package upstreamyaml

import (
	"errors"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"

	"sigs.k8s.io/yaml"
)

const (
	UpstreamOptionsFile = "upstream.yaml"
)

type UpstreamYaml struct {
	ArtifactHubPackage string         `json:"ArtifactHubPackage,omitempty"`
	ArtifactHubRepo    string         `json:"ArtifactHubRepo,omitempty"`
	AutoInstall        string         `json:"AutoInstall,omitempty"`
	ChartMetadata      chart.Metadata `json:"ChartMetadata,omitempty"`
	Deprecated         bool           `json:"Deprecated,omitempty"`
	DisplayName        string         `json:"DisplayName,omitempty"`
	Experimental       bool           `json:"Experimental,omitempty"`
	Fetch              string         `json:"Fetch,omitempty"`
	GitBranch          string         `json:"GitBranch,omitempty"`
	GitHubRelease      bool           `json:"GitHubRelease,omitempty"`
	GitRepo            string         `json:"GitRepo,omitempty"`
	GitSubdirectory    string         `json:"GitSubdirectory,omitempty"`
	HelmChart          string         `json:"HelmChart,omitempty"`
	HelmRepo           string         `json:"HelmRepo,omitempty"`
	Hidden             bool           `json:"Hidden,omitempty"`
	Namespace          string         `json:"Namespace,omitempty"`
	PackageVersion     int            `json:"PackageVersion,omitempty"`
	TrackVersions      []string       `json:"TrackVersions,omitempty"`
	ReleaseName        string         `json:"ReleaseName,omitempty"`
	Vendor             string         `json:"Vendor,omitempty"`
}

func (upstreamYaml *UpstreamYaml) setDefaults() {
	if upstreamYaml.Fetch == "" {
		upstreamYaml.Fetch = "latest"
	}

	if upstreamYaml.ReleaseName == "" {
		upstreamYaml.ReleaseName = upstreamYaml.HelmChart
	}
}

func (upstreamYaml *UpstreamYaml) validate() error {
	if upstreamYaml.Fetch != "latest" && upstreamYaml.HelmChart == "" {
		return errors.New("Fetch is latest but HelmChart is not set")
	}
	if upstreamYaml.Fetch != "latest" && upstreamYaml.HelmRepo == "" {
		return errors.New("Fetch is latest but HelmRepo is not set")
	}

	if len(upstreamYaml.TrackVersions) != 0 && upstreamYaml.HelmChart == "" {
		return errors.New("TrackVersions is set but HelmChart is not set")
	}
	if len(upstreamYaml.TrackVersions) != 0 && upstreamYaml.HelmRepo == "" {
		return errors.New("TrackVersions is set but HelmRepo is not set")
	}

	if upstreamYaml.ArtifactHubPackage != "" && upstreamYaml.ArtifactHubRepo == "" {
		return errors.New("ArtifactHubPackage is set but ArtifactHubRepo is not set")
	}
	if upstreamYaml.ArtifactHubRepo != "" && upstreamYaml.ArtifactHubPackage == "" {
		return errors.New("ArtifactHubRepo is set but ArtifactHubPackage is not set")
	}

	if upstreamYaml.GitBranch != "" && upstreamYaml.GitRepo == "" {
		return errors.New("GitBranch is set but GitRepo is not set")
	}
	if upstreamYaml.GitHubRelease && upstreamYaml.GitRepo == "" {
		return errors.New("GitHubRelease is set but GitRepo is not set")
	}
	if upstreamYaml.GitSubdirectory != "" && upstreamYaml.GitRepo == "" {
		return errors.New("GitSubdirectory is set but GitRepo is not set")
	}

	if upstreamYaml.HelmChart != "" && upstreamYaml.HelmRepo == "" {
		return errors.New("HelmChart is set but HelmRepo is not set")
	}
	if upstreamYaml.HelmRepo != "" && upstreamYaml.HelmChart == "" {
		return errors.New("HelmRepo is set but HelmChart is not set")
	}

	if !(upstreamYaml.ArtifactHubPackage != "" && upstreamYaml.ArtifactHubRepo != "" ||
		upstreamYaml.GitRepo != "" ||
		upstreamYaml.HelmRepo != "" && upstreamYaml.HelmChart != "") {
		return errors.New("must define upstream")
	}

	return nil
}

func Parse(upstreamYamlPath string) (*UpstreamYaml, error) {
	logrus.Debugf("Attempting to parse %s", upstreamYamlPath)
	contents, err := os.ReadFile(upstreamYamlPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	upstreamYaml := &UpstreamYaml{}
	if err := yaml.Unmarshal(contents, &upstreamYaml); err != nil {
		return nil, fmt.Errorf("failed to parse as YAML: %w", err)
	}

	upstreamYaml.setDefaults()

	if err := upstreamYaml.validate(); err != nil {
		return nil, fmt.Errorf("invalid upstream.yaml: %w", err)
	}

	return upstreamYaml, nil
}

func Write(upstreamYamlPath string, upstreamYaml UpstreamYaml) error {
	logrus.Debugf("Attempting to write %s", upstreamYamlPath)
	contents, err := yaml.Marshal(upstreamYaml)
	if err != nil {
		return fmt.Errorf("failed to marshal given UpstreamYaml to YAML: %w", err)
	}
	if err := os.WriteFile(upstreamYamlPath, contents, 0o644); err != nil {
		return fmt.Errorf("failed to write %q: %w", upstreamYamlPath, err)
	}
	return nil
}
