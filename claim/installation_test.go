package claim

import (
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cnabio/cnab-go/labels"
)

func TestInstallation_GetInstallationTimestamp(t *testing.T) {
	const installationName = "test"
	upgrade, err := New(installationName, ActionUpgrade, exampleBundle, exampleRef, exampleDigest, nil)
	require.NoError(t, err)
	install1, err := New(installationName, ActionInstall, exampleBundle, exampleRef, exampleDigest, nil)
	require.NoError(t, err)
	install2, err := New(installationName, ActionInstall, exampleBundle, exampleRef, exampleDigest, nil)
	require.NoError(t, err)

	t.Run("has claims", func(t *testing.T) {
		i := &Installation{Name: installationName}
		i.LoadClaims(Claims{upgrade, install1, install2})

		installTime, err := i.GetInstallationTimestamp()
		require.NoError(t, err, "GetInstallationTimestamp failed")
		assert.Equal(t, install1.Created, installTime, "invalid installation time")
	})
	t.Run("no claims", func(t *testing.T) {
		i := &Installation{Name: installationName}

		_, err := i.GetInstallationTimestamp()
		require.EqualError(t, err, "the installation test has no claims")
	})
}

func TestInstallation_GetLastClaim(t *testing.T) {
	upgrade := Claim{
		ID:     "2",
		Action: ActionUpgrade,
		results: &Results{
			{
				ID:     "1",
				Status: StatusRunning,
			},
		},
	}
	install := Claim{
		ID:     "1",
		Action: ActionInstall,
		results: &Results{
			{
				ID:     "1",
				Status: StatusSucceeded,
			},
		},
	}

	t.Run("claim exists", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}
		i.LoadClaims(Claims{upgrade, install})

		c, err := i.GetLastClaim()

		require.NoError(t, err, "GetLastClaim failed")
		assert.Equal(t, upgrade, c, "GetLastClaim did not return the expected claim")
	})

	t.Run("no claims", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}

		c, err := i.GetLastClaim()

		require.EqualError(t, err, "the installation wordpress has no claims")
		assert.Equal(t, Claim{}, c, "should return an empty claim when one cannot be found")
	})

}

func TestInstallation_GetLastResult(t *testing.T) {
	failed := Result{
		ID:     "2",
		Status: StatusFailed,
	}
	upgrade := Claim{
		ID:     "2",
		Action: ActionUpgrade,
		results: &Results{
			failed,
			{
				ID:     "1",
				Status: StatusRunning,
			},
		},
	}
	install := Claim{
		ID:     "1",
		Action: ActionInstall,
		results: &Results{
			{
				ID:     "1",
				Status: StatusSucceeded,
			},
		},
	}

	t.Run("result exists", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}
		i.LoadClaims(Claims{upgrade, install})

		r, err := i.GetLastResult()

		require.NoError(t, err, "GetLastResult failed")
		assert.Equal(t, failed, r, "GetLastResult did not return the expected result")
		assert.Equal(t, StatusFailed, i.GetLastStatus(), "GetLastStatus did not return the expected value")
	})

	t.Run("no claims", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}

		r, err := i.GetLastResult()

		require.EqualError(t, err, "the installation wordpress has no claims")
		assert.Equal(t, Result{}, r, "should return an empty result when one cannot be found")
		assert.Equal(t, StatusUnknown, i.GetLastStatus(), "GetLastStatus did not return the expected value")
	})

	t.Run("no results", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}
		i.LoadClaims(Claims{Claim{ID: "1", results: &Results{}}})

		r, err := i.GetLastResult()

		require.EqualError(t, err, "the last claim has no results")
		assert.Equal(t, Result{}, r, "should return an empty result when one cannot be found")
		assert.Equal(t, StatusUnknown, i.GetLastStatus(), "GetLastStatus did not return the expected value")
	})

	t.Run("no results loaded", func(t *testing.T) {
		i := &Installation{Name: "wordpress"}
		i.LoadClaims(Claims{Claim{ID: "1"}})

		r, err := i.GetLastResult()

		require.EqualError(t, err, "the last claim does not have any results loaded")
		assert.Equal(t, Result{}, r, "should return an empty result when one cannot be found")
		assert.Equal(t, StatusUnknown, i.GetLastStatus(), "GetLastStatus did not return the expected value")
	})
}

func TestInstallation_GetStatus(t *testing.T) {
	i := Installation{Status: InstallationStatus{ResultStatus: StatusSucceeded}}
	assert.Equal(t, StatusSucceeded, i.GetStatus())

	i = Installation{}
	assert.Equal(t, StatusUnknown, i.GetStatus())
}

func TestInstallation_GetAppAndVersion(t *testing.T) {
	i := Installation{
		Labels: map[string]string{
			labels.App:        "mysql",
			labels.AppVersion: "5.7",
		},
	}
	assert.Equal(t, "mysql", i.GetApp())
	assert.Equal(t, "5.7", i.GetAppVersion())

	i = Installation{}
	assert.Empty(t, i.GetApp())
	assert.Empty(t, i.GetAppVersion())
}

func TestInstallationByName_Sort(t *testing.T) {
	installations := InstallationByName{
		{Name: "c"},
		{Name: "a"},
		{Name: "b"},
	}

	sort.Sort(installations)

	assert.Equal(t, "a", installations[0].Name)
	assert.Equal(t, "b", installations[1].Name)
	assert.Equal(t, "c", installations[2].Name)
}

func TestInstallationByModified_Sort(t *testing.T) {
	installations := InstallationByModified{
		{Name: "c", Modified: time.Now().Add(2 * time.Hour)}, // require a sort for this to end up last (cid4 is the "oldest" timestamp)
		{Name: "a", Modified: time.Now()},
		{Name: "b", Modified: time.Now().Add(time.Hour)},
	}

	sort.Sort(installations)

	assert.Equal(t, "a", installations[0].Name)
	assert.Equal(t, "b", installations[1].Name)
	assert.Equal(t, "c", installations[2].Name)
}
