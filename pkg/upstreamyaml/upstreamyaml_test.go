package upstreamyaml

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestMain(t *testing.T) {
	t.Run("UpstreamYaml", func(t *testing.T) {
		t.Run("setDefaults", func(t *testing.T) {
			t.Run("should set Fetch field if not set", func(t *testing.T) {
				upstreamYaml := &UpstreamYaml{}
				upstreamYaml.setDefaults()
				assert.Equal(t, "latest", upstreamYaml.Fetch)
			})

			t.Run("should not change Fetch field if set", func(t *testing.T) {
				fetchValue := "newer"
				upstreamYaml := &UpstreamYaml{
					Fetch: fetchValue,
				}
				upstreamYaml.setDefaults()
				assert.Equal(t, fetchValue, upstreamYaml.Fetch)
			})

			t.Run("should set ReleaseName field if not set", func(t *testing.T) {
				helmChartValue := "helm-chart-value"
				upstreamYaml := &UpstreamYaml{
					HelmChart: helmChartValue,
				}
				upstreamYaml.setDefaults()
				assert.Equal(t, helmChartValue, upstreamYaml.ReleaseName)
			})

			t.Run("should not change ReleaseName field if set", func(t *testing.T) {
				releaseNameValue := "release-name-value"
				upstreamYaml := &UpstreamYaml{
					HelmChart:   "helm-chart-value",
					ReleaseName: releaseNameValue,
				}
				upstreamYaml.setDefaults()
				assert.Equal(t, releaseNameValue, upstreamYaml.ReleaseName)
			})
		})

		t.Run("validate", func(t *testing.T) {
			t.Run("if ArtifactHubPackage is set, ArtifactHubRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:              "latest",
					ArtifactHubPackage: "test-package",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "ArtifactHubPackage is set but ArtifactHubRepo is not set")
			})

			t.Run("if ArtifactHubRepo is set, ArtifactHubPackage must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:           "latest",
					ArtifactHubRepo: "test-repo",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "ArtifactHubRepo is set but ArtifactHubPackage is not set")
			})

			t.Run("if Fetch is not latest, HelmChart must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:    "notlatest",
					HelmRepo: "https://example.com",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "Fetch is latest but HelmChart is not set")
			})

			t.Run("if Fetch is not latest, HelmRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:     "notlatest",
					HelmChart: "test-chart",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "Fetch is latest but HelmRepo is not set")
			})

			t.Run("if GitBranch is set, GitRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:     "latest",
					GitBranch: "test-branch",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "GitBranch is set but GitRepo is not set")
			})

			t.Run("if GitHubRelease is set, GitRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:         "latest",
					GitHubRelease: true,
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "GitHubRelease is set but GitRepo is not set")
			})

			t.Run("if GitSubdirectory is set, GitRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:           "latest",
					GitSubdirectory: "test-subdirectory",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "GitSubdirectory is set but GitRepo is not set")
			})

			t.Run("if HelmChart is set, HelmRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:     "latest",
					HelmChart: "test-chart",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "HelmChart is set but HelmRepo is not set")
			})

			t.Run("if HelmRepo is set, HelmChart must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:    "latest",
					HelmRepo: "test-repo",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "HelmRepo is set but HelmChart is not set")
			})

			t.Run("if TrackVersions is set, HelmChart must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:         "latest",
					TrackVersions: []string{"2.14"},
					HelmRepo:      "https://example.com",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "TrackVersions is set but HelmChart is not set")
			})

			t.Run("if TrackVersions is set, HelmRepo must be set", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch:         "latest",
					TrackVersions: []string{"2.14"},
					HelmChart:     "test-chart",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "TrackVersions is set but HelmRepo is not set")
			})

			t.Run("one of ArtifactHubPackage and ArtifactHubRepo, GitRepo, or HelmRepo and HelmChart must be present", func(t *testing.T) {
				upstreamYaml := UpstreamYaml{
					Fetch: "latest",
				}
				err := upstreamYaml.validate()
				assert.ErrorContains(t, err, "must define upstream")
			})
		})
	})
}
