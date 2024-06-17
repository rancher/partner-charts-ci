package upstream

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/rancher/partner-charts-ci/pkg/parse"

	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
)

// A GitHelmUpstream is an Upstream that is a helm repo that is served
// via HTTPS.
type HttpsHelmUpstream struct{}

// Constructs Chart Metadata for latest version published to Helm Repository
func (h HttpsHelmUpstream) Fetch(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	return fetchUpstreamHelmRepo(upstreamYaml)
}

func fetchUpstreamHelmRepo(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	upstreamYaml.HelmRepoUrl = strings.TrimSuffix(upstreamYaml.HelmRepoUrl, "/")
	url := fmt.Sprintf("%s/index.yaml", upstreamYaml.HelmRepoUrl)

	indexYaml := repo.NewIndexFile()
	chartSourceMeta := ChartSourceMetadata{}

	if !regexp.MustCompile("^https?://").MatchString(url) {
		return chartSourceMeta, fmt.Errorf("%s (%s) invalid URL: %s", upstreamYaml.Vendor, upstreamYaml.HelmChart, url)
	}

	chartSourceMeta.Source = "HelmRepo"

	resp, err := http.Get(url)
	if err != nil {
		return chartSourceMeta, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chartSourceMeta, err
	}

	err = yaml.Unmarshal([]byte(body), indexYaml)
	if err != nil {
		return chartSourceMeta, err
	}
	if _, ok := indexYaml.Entries[upstreamYaml.HelmChart]; !ok {
		return chartSourceMeta, fmt.Errorf("Helm chart: %s/%s not found", upstreamYaml.HelmRepoUrl, upstreamYaml.HelmChart)
	}

	indexYaml.SortEntries()
	upstreamVersions := indexYaml.Entries[upstreamYaml.HelmChart]

	for i := range upstreamVersions {
		chartUrl := upstreamVersions[i].URLs[0]
		if !strings.HasPrefix(chartUrl, "http") {
			upstreamVersions[i].URLs[0] = upstreamYaml.HelmRepoUrl + "/" + chartUrl
		}
	}

	chartSourceMeta.Versions = indexYaml.Entries[upstreamYaml.HelmChart]

	return chartSourceMeta, nil
}
