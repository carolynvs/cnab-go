package kubernetes

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/hashicorp/go-multierror"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	batchv1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	batchclientv1 "k8s.io/client-go/kubernetes/typed/batch/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	// load credential helpers
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/driver"
)

const (
	k8sContainerName      = "invocation"
	numBackoffLoops       = 6
	cnabPrefix            = "cnab.io/"
	SettingInCluster      = "IN_CLUSTER"
	SettingCleanupJobs    = "CLEANUP_JOBS"
	SettingLabels         = "LABELS"
	SettingJobVolumePath  = "JOB_VOLUME_PATH"
	SettingJobVolumeName  = "JOB_VOLUME_NAME"
	SettingKubeNamespace  = "KUBE_NAMESPACE"
	SettingServiceAccount = "SERVICE_ACCOUNT"
	SettingKubeconfig     = "KUBECONFIG"
	SettingMasterUrl      = "MASTER_URL"
)

var (
	dns1123Reg = regexp.MustCompile(`[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*`)
)

// Driver runs an invocation image in a Kubernetes cluster.
type Driver struct {
	Namespace             string
	ServiceAccountName    string
	Annotations           map[string]string
	Labels                []string
	LimitCPU              resource.Quantity
	LimitMemory           resource.Quantity
	JobVolumePath         string
	JobVolumeName         string
	Tolerations           []v1.Toleration
	ActiveDeadlineSeconds int64
	BackoffLimit          int32
	SkipCleanup           bool
	skipJobStatusCheck    bool
	jobs                  batchclientv1.JobInterface
	secrets               coreclientv1.SecretInterface
	pods                  coreclientv1.PodInterface
	deletionPolicy        metav1.DeletionPropagation
}

// New initializes a Kubernetes driver.
func New(namespace, serviceAccount string, conf *rest.Config) (*Driver, error) {
	driver := &Driver{
		Namespace:          namespace,
		ServiceAccountName: serviceAccount,
	}
	driver.setDefaults()
	err := driver.setClient(conf)
	return driver, err
}

// Handles receives an ImageType* and answers whether this driver supports that type.
func (k *Driver) Handles(imagetype string) bool {
	return imagetype == driver.ImageTypeDocker || imagetype == driver.ImageTypeOCI
}

// Config returns the Kubernetes driver configuration options.
func (k *Driver) Config() map[string]string {
	return map[string]string{
		SettingInCluster:      "Connect to the cluster using in-cluster environment variables",
		SettingCleanupJobs:    "If true, the job and associated secrets will be destroyed when it finishes running. If false, it will not be destroyed. The supported values are true and false. Defaults to true.",
		SettingLabels:         "Labels to apply to cluster resources created by the driver, separated by whitespace.",
		SettingJobVolumePath:  "Path where the persistent volume is mounted",
		SettingJobVolumeName:  "Name of the PersistentVolumeClaim to mount which enables the driver to share files with the invocation image",
		SettingKubeNamespace:  "Kubernetes namespace in which to run the invocation image",
		SettingServiceAccount: "Kubernetes service account to be mounted by the invocation image (if empty, no service account token will be mounted)",
		SettingKubeconfig:     "Absolute path to the kubeconfig file",
		SettingMasterUrl:      "Kubernetes master endpoint",
	}
}

// SetConfig sets Kubernetes driver configuration.
func (k *Driver) SetConfig(settings map[string]string) error {
	k.setDefaults()
	k.Namespace = settings[SettingKubeNamespace]
	k.ServiceAccountName = settings[SettingServiceAccount]
	k.Labels = strings.Split(settings[SettingLabels], " ")

	k.JobVolumePath = settings[SettingJobVolumePath]
	if k.JobVolumePath == "" {
		return errors.Errorf("setting %s is required", SettingJobVolumePath)
	}
	k.JobVolumeName = settings[SettingJobVolumeName]
	if k.JobVolumeName == "" {
		return errors.Errorf("setting %s is required", SettingJobVolumeName)
	}

	cleanup, err := strconv.ParseBool(settings[SettingCleanupJobs])
	if err == nil {
		k.SkipCleanup = !cleanup
	}

	var conf *rest.Config
	if incluster, _ := strconv.ParseBool(settings[SettingInCluster]); incluster {
		conf, err = rest.InClusterConfig()
		if err != nil {
			return errors.Wrap(err, "error retrieving in-cluster kubernetes configuration")
		}
	} else {
		var kubeconfig string
		if kpath := settings[SettingKubeconfig]; kpath != "" {
			kubeconfig = kpath
		} else if home := homeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}

		conf, err = clientcmd.BuildConfigFromFlags(settings[SettingMasterUrl], kubeconfig)
		if err != nil {
			return errors.Wrapf(err, "error retrieving external kubernetes configuration using configuration:\n%v", settings)
		}
	}

	return k.setClient(conf)
}

func (k *Driver) setDefaults() {
	k.SkipCleanup = false
	k.BackoffLimit = 0
	k.ActiveDeadlineSeconds = 300
	k.deletionPolicy = metav1.DeletePropagationBackground
}

func (k *Driver) setClient(conf *rest.Config) error {
	coreClient, err := coreclientv1.NewForConfig(conf)
	if err != nil {
		return errors.Wrap(err, "error creating CoreClient for Kubernetes Driver")
	}
	batchClient, err := batchclientv1.NewForConfig(conf)
	if err != nil {
		return errors.Wrap(err, "error creating BatchClient for Kubernetes Driver")
	}
	k.jobs = batchClient.Jobs(k.Namespace)
	k.secrets = coreClient.Secrets(k.Namespace)
	k.pods = coreClient.Pods(k.Namespace)

	return nil
}

// Run executes the operation inside of the invocation image.
func (k *Driver) Run(op *driver.Operation) (driver.OperationResult, error) {
	if k.Namespace == "" {
		return driver.OperationResult{}, fmt.Errorf("KUBE_NAMESPACE is required")
	}

	const sharedVolumeName = "cnab-driver-share"
	err = k.initJobVolumes(err)
	if err != nil {
		return driver.OperationResult{}, err
	}

	meta := metav1.ObjectMeta{
		Namespace:    k.Namespace,
		GenerateName: generateNameTemplate(op),
		Labels: map[string]string{
			"cnab.io/driver": "kubernetes",
		},
		Annotations: generateMergedAnnotations(op, k.Annotations),
	}

	// Apply custom labels
	for _, l := range k.Labels {
		parts := strings.SplitN(l, "=", 2)
		if len(parts) > 1 {
			meta.Labels[parts[0]] = parts[1]
		}
	}

	// Mount SA token if a non-zero value for ServiceAccountName has been specified
	mountServiceAccountToken := k.ServiceAccountName != ""

	job := &batchv1.Job{
		ObjectMeta: meta,
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds: &k.ActiveDeadlineSeconds,
			Completions:           defaultInt32Ptr(1),
			BackoffLimit:          &k.BackoffLimit,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      meta.Labels,
					Annotations: meta.Annotations,
				},
				Spec: v1.PodSpec{
					ServiceAccountName:           k.ServiceAccountName,
					AutomountServiceAccountToken: &mountServiceAccountToken,
					RestartPolicy:                v1.RestartPolicyNever,
					Tolerations:                  k.Tolerations,
					Volumes: []v1.Volume{
						// This is a shared volume between the driver and the job so that files be shared
						{
							Name: sharedVolumeName,
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
									ClaimName: k.JobVolumeName,
								},
							},
						},
					},
				},
			},
		},
	}
	img, err := imageWithDigest(op.Image)
	if err != nil {
		return driver.OperationResult{}, err
	}

	container := v1.Container{
		Name:    k8sContainerName,
		Image:   img,
		Command: []string{"/cnab/app/run"},
		Resources: v1.ResourceRequirements{
			Limits: v1.ResourceList{
				v1.ResourceCPU:    k.LimitCPU,
				v1.ResourceMemory: k.LimitMemory,
			},
		},
		ImagePullPolicy: v1.PullIfNotPresent,
		VolumeMounts: []v1.VolumeMount{
			{
				Name:      sharedVolumeName,
				MountPath: "/cnab/app/outputs",
				SubPath:   "outputs",
			},
		},
	}

	if len(op.Environment) > 0 {
		secret := &v1.Secret{
			ObjectMeta: meta,
			StringData: op.Environment,
		}
		secret.ObjectMeta.GenerateName += "env-"
		secret, err := k.secrets.Create(secret)
		if err != nil {
			return driver.OperationResult{}, err
		}
		if !k.SkipCleanup {
			defer k.deleteSecret(secret.ObjectMeta.Name)
		}

		container.EnvFrom = []v1.EnvFromSource{
			{
				SecretRef: &v1.SecretEnvSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: secret.ObjectMeta.Name,
					},
				},
			},
		}
	}

	if len(op.Files) > 0 {
		// Write the files to the inputs directory on the shared volume and mount them individually to the desired location in the invocation image
		for inputRelPath, contents := range op.Files {
			inputPath := filepath.Join(k.JobVolumePath, "inputs", inputRelPath)
			err = os.MkdirAll(filepath.Dir(inputPath), 0700)
			if err != nil {
				return driver.OperationResult{}, errors.Wrapf(err, "error creating directory for file %s on the shared job volume %s", inputPath, k.JobVolumeName)
			}
			err = ioutil.WriteFile(inputPath, []byte(contents), 0600)
			if err != nil {
				return driver.OperationResult{}, errors.Wrapf(err, "error writing file %s to the shared job volume %s", inputPath, k.JobVolumeName)
			}

			container.VolumeMounts = append(container.VolumeMounts, v1.VolumeMount{
				Name:      sharedVolumeName,
				MountPath: inputRelPath,
				SubPath:   path.Join("inputs", inputRelPath),
			})
		}
	}

	job.Spec.Template.Spec.Containers = []v1.Container{container}

	job, err = k.jobs.Create(job)
	if err != nil {
		return driver.OperationResult{}, err
	}
	if !k.SkipCleanup {
		defer k.deleteJob(job.ObjectMeta.Name)
	}

	// Skip waiting for the job in unit tests (the fake k8s client implementation just
	// hangs during watch because no events are ever created on the Job)
	var opErr *multierror.Error
	if !k.skipJobStatusCheck {
		// Create a selector to detect the job just created
		jobSelector := metav1.ListOptions{
			LabelSelector: labels.Set(job.ObjectMeta.Labels).String(),
			FieldSelector: newSingleFieldSelector("metadata.name", job.ObjectMeta.Name),
		}

		// Prevent detecting pods from prior jobs by adding the job name to the labels
		podSelector := metav1.ListOptions{
			LabelSelector: newSingleFieldSelector("job-name", job.ObjectMeta.Name),
		}

		err = k.watchJobStatusAndLogs(podSelector, jobSelector, op.Out)
		if err != nil {
			opErr = multierror.Append(opErr, errors.Wrapf(err, "job %s failed", job.Name))
		}
	}

	opResult, err := k.fetchOutputs(op)
	if err != nil {
		opErr = multierror.Append(opErr, err)
	}

	return opResult, opErr.ErrorOrNil()
}

func (k *Driver) initJobVolumes(err error) error {
	// Store all job input files in ./inputs and outputs in ./outputs on the shared volume

	inputsDir := filepath.Join(k.JobVolumePath, "inputs")
	err = os.Mkdir(inputsDir, 0700)
	if err != nil && !os.IsExist(err) {
		return errors.Wrapf(err, "error creating inputs directory %s on shared job volume %s", inputsDir, k.JobVolumeName)
	}

	outputsDir := filepath.Join(k.JobVolumePath, "outputs")
	err = os.Mkdir(outputsDir, 0700)
	if err != nil && !os.IsExist(err) {
		return errors.Wrapf(err, "error creating outputs directory %s on shared job volume %s", outputsDir, k.JobVolumeName)
	}

	return nil
}

// fetchOutputs collects any outputs created by the job that were persisted to JobVolumeName (which is mounted locally
// at JobVolumePath).
//
// The goal is to collect all the files in the directory (recursively) and put them in a flat map of path to contents.
// This map will be inside the OperationResult. When fetchOutputs returns an error, it may also return partial results.
func (k *Driver) fetchOutputs(op *driver.Operation) (driver.OperationResult, error) {
	opResult := driver.OperationResult{
		Outputs: map[string]string{},
	}

	if len(op.Bundle.Outputs) == 0 {
		return opResult, nil
	}

	outputsDir := filepath.Join(k.JobVolumePath, "outputs")
	err := filepath.Walk(outputsDir, func(currentPath string, info os.FileInfo, err error) error {
		// skip directories because we're gathering file contents
		if info.IsDir() {
			return nil
		}

		var contents []byte
		pathInContainer := path.Join("/cnab/app/outputs", info.Name())
		outputName, shouldCapture := op.Outputs[pathInContainer]
		if shouldCapture {
			contents, err = ioutil.ReadFile(currentPath)
			if err != nil {
				return errors.Wrapf(err, "error while reading %q from outputs", pathInContainer)
			}
			opResult.Outputs[outputName] = string(contents)
		}

		return nil
	})

	return opResult, err
}

func (k *Driver) watchJobStatusAndLogs(podSelector metav1.ListOptions, jobSelector metav1.ListOptions, out io.Writer) error {
	// Stream Pod logs in the background
	logsStreamingComplete := make(chan bool)
	err := k.streamPodLogs(podSelector, out, logsStreamingComplete)
	if err != nil {
		return err
	}
	// Watch job events and exit on failure/success
	watch, err := k.jobs.Watch(jobSelector)
	if err != nil {
		return err
	}
	for event := range watch.ResultChan() {
		job, ok := event.Object.(*batchv1.Job)
		if !ok {
			return fmt.Errorf("unexpected type")
		}
		complete := false
		for _, cond := range job.Status.Conditions {
			if cond.Type == batchv1.JobFailed {
				err = fmt.Errorf(cond.Message)
				complete = true
				break
			}
			if cond.Type == batchv1.JobComplete {
				complete = true
				break
			}
		}
		if complete {
			break
		}
	}

	// Wait for pod logs to finish printing
	<-logsStreamingComplete

	return err
}

func (k *Driver) streamPodLogs(options metav1.ListOptions, out io.Writer, done chan bool) error {
	watcher, err := k.pods.Watch(options)
	if err != nil {
		return err
	}

	go func() {
		// Track pods whose logs have been streamed by pod name. We need to know when we've already
		// processed logs for a given pod, since multiple lifecycle events are received per pod.
		streamedLogs := map[string]bool{}
		for event := range watcher.ResultChan() {
			pod, ok := event.Object.(*v1.Pod)
			if !ok {
				continue
			}
			podName := pod.GetName()
			if streamedLogs[podName] {
				// The event was for a pod whose logs have already been streamed, so do nothing.
				continue
			}

			for i := 0; i < numBackoffLoops; i++ {
				time.Sleep(time.Duration(i*i/2) * time.Second)
				req := k.pods.GetLogs(podName, &v1.PodLogOptions{
					Container: k8sContainerName,
					Follow:    true,
				})
				reader, err := req.Stream()
				if err != nil {
					// There was an error connecting to the pod, so continue the loop and attempt streaming
					// the logs again.
					continue
				}

				// Block the loop until all logs from the pod have been processed.
				bytesRead, err := io.Copy(out, reader)
				reader.Close()
				if err != nil {
					continue
				}
				if bytesRead == 0 {
					// There is a chance where we have connected to the pod, but it has yet to write something.
					// In that case, we continue to to keep streaming until it does.
					continue
				}
				// Set the pod to have successfully streamed data.
				streamedLogs[podName] = true
				break
			}

			done <- true
		}
	}()

	return nil
}

func (k *Driver) deleteSecret(name string) error {
	return k.secrets.Delete(name, &metav1.DeleteOptions{
		PropagationPolicy: &k.deletionPolicy,
	})
}

func (k *Driver) deleteJob(name string) error {
	return k.jobs.Delete(name, &metav1.DeleteOptions{
		PropagationPolicy: &k.deletionPolicy,
	})
}

const maxNameTemplateLength = 50

// generateNameTemplate returns a value suitable for the Kubernetes metav1.ObjectMeta.GenerateName
// field, that includes the operation action and installation names for debugging purposes.
//
// Note that the value returned may be truncated to conform to Kubernetes maximum resource name
// length constraints.
func generateNameTemplate(op *driver.Operation) string {
	const maxLength = maxNameTemplateLength - 1
	name := fmt.Sprintf("%s-%s", op.Action, op.Installation)
	if len(name) > maxLength {
		name = name[0:maxLength]
	}

	var result string
	for _, match := range dns1123Reg.FindAllString(strings.ToLower(name), maxLength) {
		// It's safe to add one character because we've already removed at least one character not matching our regex.
		result += match + "-"
	}

	return result
}

func generateMergedAnnotations(op *driver.Operation, mergeWith map[string]string) map[string]string {
	anno := map[string]string{
		"cnab.io/installation": op.Installation,
		"cnab.io/action":       op.Action,
		"cnab.io/revision":     op.Revision,
	}

	for k, v := range mergeWith {
		if strings.HasPrefix(k, cnabPrefix) {
			log.Printf("Annotations with prefix '%s' are reserved. Annotation '%s: %s' will not be applied.\n", cnabPrefix, k, v)
			continue
		}
		anno[k] = v
	}

	return anno
}

func newSingleFieldSelector(k, v string) string {
	return labels.Set(map[string]string{
		k: v,
	}).String()
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

func imageWithDigest(img bundle.InvocationImage) (string, error) {
	// img.Image can be just the name, name:tag or name@digest
	ref, err := reference.ParseNormalizedNamed(img.Image)
	if err != nil {
		return "", errors.Wrapf(err, "could not parse %s as an OCI reference", img.Image)
	}

	var d digest.Digest
	if v, ok := ref.(reference.Digested); ok {
		// Check that the digests match since it's provided twice
		if img.Digest != "" && img.Digest != v.Digest().String() {
			return "", errors.Errorf("The digest %s for the image %s doesn't match the one specified in the image", img.Digest, img.Image)
		}
		d = v.Digest()
	} else if img.Digest != "" {
		d, err = digest.Parse(img.Digest)
		if err != nil {
			return "", errors.Wrapf(err, "invalid digest %s specified for invocation image %s", img.Digest, img.Image)
		}
	}

	// Digest was not supplied anywhere
	if d == "" {
		return img.Image, nil
	}

	digestedRef, err := reference.WithDigest(ref, d)
	return reference.FamiliarString(digestedRef), errors.Wrapf(err, "invalid image digest %s", d.String())
}
