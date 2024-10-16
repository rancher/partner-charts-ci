package upstreamyaml

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"

	"sigs.k8s.io/yaml"
)

const (
	UpstreamOptionsFile = "upstream.yaml"
)

type UpstreamYaml struct {
	AHPackageName   string         `json:"ArtifactHubPackage,omitempty"`
	AHRepoName      string         `json:"ArtifactHubRepo,omitempty"`
	AutoInstall     string         `json:"AutoInstall,omitempty"`
	ChartYaml       chart.Metadata `json:"ChartMetadata,omitempty"`
	Deprecated      bool           `json:"Deprecated,omitempty"`
	DisplayName     string         `json:"DisplayName,omitempty"`
	Experimental    bool           `json:"Experimental,omitempty"`
	Fetch           string         `json:"Fetch,omitempty"`
	GitBranch       string         `json:"GitBranch,omitempty"`
	GitHubRelease   bool           `json:"GitHubRelease,omitempty"`
	GitRepoUrl      string         `json:"GitRepo,omitempty"`
	GitSubDirectory string         `json:"GitSubdirectory,omitempty"`
	HelmChart       string         `json:"HelmChart,omitempty"`
	HelmRepoUrl     string         `json:"HelmRepo,omitempty"`
	Hidden          bool           `json:"Hidden,omitempty"`
	Namespace       string         `json:"Namespace,omitempty"`
	PackageVersion  int            `json:"PackageVersion,omitempty"`
	TrackVersions   []string       `json:"TrackVersions,omitempty"`
	ReleaseName     string         `json:"ReleaseName,omitempty"`
	Vendor          string         `json:"Vendor,omitempty"`
}

func Parse(packagePath string) (UpstreamYaml, error) {
	upstreamYamlPath := filepath.Join(packagePath, UpstreamOptionsFile)
	logrus.Debugf("Attempting to parse %s", upstreamYamlPath)
	upstreamYamlFile, err := os.ReadFile(upstreamYamlPath)
	upstreamYaml := UpstreamYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &upstreamYaml)
	}

	return upstreamYaml, err
}

func Write(packagePath string, upstreamYaml UpstreamYaml) error {
	upstreamYamlPath := filepath.Join(packagePath, UpstreamOptionsFile)
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
