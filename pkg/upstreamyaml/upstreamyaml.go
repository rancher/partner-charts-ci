package upstreamyaml

import (
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
	HelmRepoUrl        string         `json:"HelmRepo,omitempty"`
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

	return upstreamYaml, err
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
