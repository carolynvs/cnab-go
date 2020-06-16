package kubernetes

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/driver"
)

func TestDriver_Run(t *testing.T) {
	client := fake.NewSimpleClientset()
	namespace := "default"
	k := Driver{
		Namespace:          namespace,
		jobs:               client.BatchV1().Jobs(namespace),
		secrets:            client.CoreV1().Secrets(namespace),
		pods:               client.CoreV1().Pods(namespace),
		SkipCleanup:        true,
		skipJobStatusCheck: true,
	}
	op := driver.Operation{
		Action: "install",
		Out:    os.Stdout,
		Environment: map[string]string{
			"foo": "bar",
		},
	}

	_, err := k.Run(context.Background(), &op)
	assert.NoError(t, err)

	jobList, _ := k.jobs.List(metav1.ListOptions{})
	assert.Equal(t, len(jobList.Items), 1, "expected one job to be created")

	secretList, _ := k.secrets.List(metav1.ListOptions{})
	assert.Equal(t, len(secretList.Items), 1, "expected one secret to be created")
}

func TestImageWithDigest(t *testing.T) {
	testCases := map[string]bundle.InvocationImage{
		"foo": {
			BaseImage: bundle.BaseImage{
				Image: "foo",
			},
		},
		"foo/bar": {
			BaseImage: bundle.BaseImage{
				Image: "foo/bar",
			},
		},
		"foo/bar:baz": {
			BaseImage: bundle.BaseImage{
				Image: "foo/bar:baz",
			},
		},
		"foo/bar:baz@sha:a1b2c3": {
			BaseImage: bundle.BaseImage{
				Image:  "foo/bar:baz",
				Digest: "sha:a1b2c3",
			},
		},
		"foo/bar@sha:a1b2c3": {
			BaseImage: bundle.BaseImage{
				Image:  "foo/bar",
				Digest: "sha:a1b2c3",
			},
		},
	}

	for expectedImageRef, img := range testCases {
		t.Run(expectedImageRef, func(t *testing.T) {
			assert.Equal(t, expectedImageRef, imageWithDigest(img))
		})
	}
}

func TestGenerateNameTemplate(t *testing.T) {
	testCases := map[string]struct {
		op       *driver.Operation
		expected string
	}{
		"short name": {
			op: &driver.Operation{
				Action:       "install",
				Installation: "foo",
			},
			expected: "install-foo-",
		},
		"special chars": {
			op: &driver.Operation{
				Action:       "example.com/liftoff",
				Installation: "🚀 me to the 🌙",
			},
			expected: "example.com-liftoff-me-to-the-",
		},
		"long installation name": {
			op: &driver.Operation{
				Action:       "install",
				Installation: "this-should-be-truncated-qcUYSfR9MS3BqR0kRDHe2K5EHJa8BJGrcoiDVvsDpATjIkr",
			},
			expected: "install-this-should-be-truncated-qcuysfr9ms3bqr0k-",
		},
		"maximum matching segments": {
			op: &driver.Operation{
				Action:       "a",
				Installation: "b c d e f g h i j k l m n o p q r s t u v w x y z",
			},
			expected: "a-b-c-d-e-f-g-h-i-j-k-l-m-n-o-p-q-r-s-t-u-v-w-x-y-",
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			actual := generateNameTemplate(tc.op)
			assert.Equal(t, tc.expected, actual)
			assert.True(t, len(actual) <= maxNameTemplateLength)
		})
	}
}
