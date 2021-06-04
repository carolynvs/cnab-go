package claim

import (
	"fmt"
	"sort"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/pkg/errors"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/labels"
	"github.com/cnabio/cnab-go/schema"
)

// Installation represents the installation of a bundle.
type Installation struct {
	// SchemaVersion is the version of the installation state schema.
	SchemaVersion schema.Version `json:"schemaVersion"`

	// Name of the installation.
	Name string `json:"name"`

	// Namespace in which the installation is defined.
	Namespace string `json:"namespace,omitempty"`

	// BundleRepository is the OCI repository of the current bundle definition.
	BundleRepository string `json:"bundleRepository,omitempty"`

	// BundleVersion is the current version of the bundle.
	BundleVersion string `json:"bundleVersion"`

	// BundleDigest is the current digest of the bundle.
	BundleDigest string `json:"bundleDigest,omitempty"`

	// Created timestamp of the installation.
	Created time.Time `json:"created"`

	// Modified timestamp of the installation.
	Modified time.Time `json:"modified"`

	// Custom extension data applicable to a given runtime.
	Custom interface{} `json:"custom,omitempty"`

	// Labels applied to the installation.
	Labels map[string]string `json:"labels,omitempty"`

	// Status of the installation.
	Status InstallationStatus `json:"status"`

	claims Claims `json:"-"`
}

type InstallationStatus struct {
	// ClaimID of the claim that last informed the installation status.
	ClaimID string `json:"claimID"`

	// Action of the claim that last informed the installation status.
	Action string `json:"action"`

	// Revision of the installation.
	Revision string `json:"revision"`

	// ResultID of the result that last informed the installation status.
	ResultID string `json:"resultID"`

	// ResultStatus is the status of the result that last informed the installation status.
	ResultStatus string `json:"resultStatus"`
}

// NewInstallation creates a new Installation document.
func NewInstallation(namespace string, name string, bundle bundle.Bundle, bundleRef string, bundleDigest string) (Installation, error) {
	if !ValidName.MatchString(name) {
		return Installation{}, errors.Errorf("invalid installation name '%s'. Names must be [a-zA-Z0-9-_]+", name)
	}

	schemaVersion, err := GetDefaultSchemaVersion()
	if err != nil {
		return Installation{}, err
	}

	now := time.Now()

	var repo string
	if bundleRef != "" {
		ref, err := reference.ParseNormalizedNamed(bundleRef)
		if err != nil {
			return Installation{}, errors.Wrapf(err, "invalid bundle reference '%s'", bundleRef)
		}
		repo = ref.Name()
	}

	labels := make(map[string]string, len(bundle.Labels))
	for k, v := range bundle.Labels {
		labels[k] = v
	}

	return Installation{
		SchemaVersion:    schemaVersion,
		Name:             name,
		Namespace:        namespace,
		Created:          now,
		Modified:         now,
		BundleRepository: repo,
		BundleVersion:    bundle.Version,
		BundleDigest:     bundleDigest,
	}, nil
}

// NewInstallation creates an Installation and ensures the contained data is sorted.
func (i *Installation) LoadClaims(claims []Claim) {
	i.claims = claims
	sort.Sort(i.claims)
	for _, c := range i.claims {
		if c.results != nil {
			sort.Sort(c.results)
		}
	}
}

// GetApp returns the name of the application represented by the bundle, if defined.
func (i Installation) GetApp() string {
	return i.Labels[labels.App]
}

// GetAppVersion returns the version of the application represented by the bundle, if defined.
func (i Installation) GetAppVersion() string {
	return i.Labels[labels.AppVersion]
}

// GetInstallationTimestamp searches the claims associated with the installation
// for the first claim for Install and returns its timestamp.
// DEPRECATED: Use Installation.Created instead.
func (i Installation) GetInstallationTimestamp() (time.Time, error) {
	if len(i.claims) == 0 {
		return time.Time{}, fmt.Errorf("the installation %s has no claims", i.Name)
	}

	for _, c := range i.claims {
		if c.Action == ActionInstall {
			return c.Created, nil
		}
	}

	return time.Time{}, fmt.Errorf("the installation %s has never been installed", i.Name)
}

// GetLastClaim returns the most recent (last) claim associated with the
// installation.
// DEPRECATED: Use Installation.Status.ClaimID instead.
func (i Installation) GetLastClaim() (Claim, error) {
	if len(i.claims) == 0 {
		return Claim{}, fmt.Errorf("the installation %s has no claims", i.Name)
	}

	lastClaim := i.claims[len(i.claims)-1]
	return lastClaim, nil
}

// GetLastResult returns the most recent (last) result associated with the
// installation.
// DEPRECATED: Use Installation.Status.ResultID instead.
func (i Installation) GetLastResult() (Result, error) {
	lastClaim, err := i.GetLastClaim()
	if err != nil {
		return Result{}, err
	}

	if lastClaim.results == nil {
		return Result{}, errors.New("the last claim does not have any results loaded")
	}

	results := *lastClaim.results
	if len(results) == 0 {
		return Result{}, errors.New("the last claim has no results")
	}

	lastResult := results[len(results)-1]
	return lastResult, nil
}

// GetLastStatus returns the status from the most recent (last) result
// associated with the installation, or "unknown" if it cannot be determined.
// DEPRECATED: Use Installation.GetStatus() instead.
func (i Installation) GetLastStatus() string {
	lastResult, err := i.GetLastResult()
	if err != nil {
		return StatusUnknown
	}

	return lastResult.Status
}

// GetStatus returns the last known status of the installation.
func (i Installation) GetStatus() string {
	if i.Status.ResultStatus == "" {
		return StatusUnknown
	}
	return i.Status.ResultStatus
}

// ApplyClaim to the installation, updating the installation to match the
// bundle operation about to be executed.
func (i Installation) ApplyClaim(c Claim) Installation {
	i.BundleVersion = c.Bundle.Version
	i.BundleDigest = c.BundleDigest
	if ref, err := reference.ParseNormalizedNamed(c.BundleReference); err == nil {
		i.BundleRepository = ref.Name()
	}

	if i.Labels == nil {
		i.Labels = make(map[string]string, len(c.Bundle.Labels))
	}
	for k, v := range c.Bundle.Labels {
		i.Labels[k] = v
	}

	i.Status = InstallationStatus{
		ClaimID:  c.ID,
		Revision: c.Revision,
		Action:   c.Action,
	}

	return i
}

// ApplyResult to the installation, updating the installation status
// to match the latest result.
func (i Installation) ApplyResult(r Result) Installation {
	i.Status.ResultID = r.ID
	i.Status.ResultStatus = r.Status

	return i
}

type InstallationByName []Installation

func (ibn InstallationByName) Len() int {
	return len(ibn)
}

func (ibn InstallationByName) Less(i, j int) bool {
	return ibn[i].Name < ibn[j].Name
}

func (ibn InstallationByName) Swap(i, j int) {
	ibn[i], ibn[j] = ibn[j], ibn[i]
}

// InstallationByModified sorts installations by which has been modified most recently
// Assumes that the installation's claims have already been sorted first, for example
// with SortClaims or manually.
type InstallationByModified []Installation

func (ibm InstallationByModified) Len() int {
	return len(ibm)
}

func (ibm InstallationByModified) Less(i, j int) bool {
	return ibm[i].Modified.Before(ibm[j].Modified)
}

func (ibm InstallationByModified) Swap(i, j int) {
	ibm[i], ibm[j] = ibm[j], ibm[i]
}
