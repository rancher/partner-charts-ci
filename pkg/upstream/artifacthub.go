package upstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rancher/partner-charts-ci/pkg/parse"
)

const (
	artifactHubApi = "https://artifacthub.io/api/v1/packages/helm"
)

type ArtifactHubApiHelmRepo struct {
	OrgDisplayName string `json:"organization_display_name,omitempty"`
	OrgName        string `json:"organization_name,omitempty"`
	Url            string `json:"url"`
}

type ArtifactHubApiHelm struct {
	ContentUrl string                 `json:"content_url"`
	Name       string                 `json:"name"`
	Repository ArtifactHubApiHelmRepo `json:"repository"`
}

// An ArtifactHubApiUpstream is an Upstream that gets helm charts from an
// artifacthub.io repo.
type ArtifactHubApiUpstream struct{}

// Constructs Chart Metadata for latest version published to ArtifactHub
func (a ArtifactHubApiUpstream) Fetch(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/%s/%s", artifactHubApi, upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)

	apiResp := ArtifactHubApiHelm{}

	resp, err := http.Get(url)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	err = json.Unmarshal([]byte(body), &apiResp)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	if apiResp.ContentUrl == "" {
		return ChartSourceMetadata{}, fmt.Errorf("ArtifactHub package: %s/%s not found", upstreamYaml.AHRepoName, upstreamYaml.AHPackageName)
	}

	upstreamYaml.HelmRepoUrl = apiResp.Repository.Url
	upstreamYaml.HelmChart = apiResp.Name

	chartSourceMeta, err := fetchUpstreamHelmRepo(upstreamYaml)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	chartSourceMeta.Source = "ArtifactHub"

	return chartSourceMeta, nil
}
