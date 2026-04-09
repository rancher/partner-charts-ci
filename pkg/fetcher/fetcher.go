package fetcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-github/v66/github"
	"github.com/rancher/partner-charts-ci/pkg/upstreamyaml"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"

	"sigs.k8s.io/yaml"
)

const (
	artifactHubAPI = "https://artifacthub.io/api/v1/packages/helm"
)

type ArtifactHubAPIHelmRepo struct {
	OrgDisplayName string `json:"organization_display_name,omitempty"`
	OrgName        string `json:"organization_name,omitempty"`
	URL            string `json:"url"`
}

type ArtifactHubAPIHelm struct {
	ContentURL string                 `json:"content_url"`
	Name       string                 `json:"name"`
	Repository ArtifactHubAPIHelmRepo `json:"repository"`
}

type ChartSourceMetadata struct {
	Commit       string
	Source       string
	SubDirectory string
	Versions     repo.ChartVersions
}

// Constructs Chart Metadata for latest version published to Helm Repository
func fetchUpstreamHelmrepo(upstreamYaml upstreamyaml.UpstreamYaml) (ChartSourceMetadata, error) {
	upstreamYaml.HelmRepo = strings.TrimSuffix(upstreamYaml.HelmRepo, "/")
	url := fmt.Sprintf("%s/index.yaml", upstreamYaml.HelmRepo)

	indexYaml := repo.NewIndexFile()
	chartSourceMeta := ChartSourceMetadata{}

	if !regexp.MustCompile("^https?://").MatchString(url) {
		return chartSourceMeta, fmt.Errorf("%s (%s) invalid URL: %s", upstreamYaml.Vendor, upstreamYaml.HelmChart, url)
	}

	chartSourceMeta.Source = "HelmRepo"

	resp, err := http.Get(url)
	if err != nil {
		return chartSourceMeta, fmt.Errorf("request to %s failed: %w", url, err)
	}
	if resp.StatusCode > 200 && resp.StatusCode < 300 {
		// if 2xx response, it should be 200, but we want to know if it isn't so
		// we can handle it
		logrus.Warnf("request to %s returned response %q", url, resp.Status)
	}
	if resp.StatusCode >= 300 {
		return chartSourceMeta, fmt.Errorf("request to %s returned response %q", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return chartSourceMeta, fmt.Errorf("failed to read response body: %w", err)
	}

	err = yaml.Unmarshal([]byte(body), indexYaml)
	if err != nil {
		return chartSourceMeta, fmt.Errorf("failed to unmarshal response body: %w", err)
	}
	if _, ok := indexYaml.Entries[upstreamYaml.HelmChart]; !ok {
		return chartSourceMeta, fmt.Errorf("helm chart: %s/%s not found", upstreamYaml.HelmRepo, upstreamYaml.HelmChart)
	}

	indexYaml.SortEntries()
	upstreamVersions := indexYaml.Entries[upstreamYaml.HelmChart]

	for i := range upstreamVersions {
		chartURL := upstreamVersions[i].URLs[0]
		if !strings.HasPrefix(chartURL, "http") {
			upstreamVersions[i].URLs[0] = upstreamYaml.HelmRepo + "/" + chartURL
		}
	}

	chartSourceMeta.Versions = indexYaml.Entries[upstreamYaml.HelmChart]

	return chartSourceMeta, nil
}

// Constructs Chart Metadata for latest version published to ArtifactHub
func fetchUpstreamArtifacthub(upstreamYaml upstreamyaml.UpstreamYaml) (ChartSourceMetadata, error) {
	url := fmt.Sprintf("%s/%s/%s", artifactHubAPI, upstreamYaml.ArtifactHubRepo, upstreamYaml.ArtifactHubPackage)

	resp, err := http.Get(url)
	if err != nil {
		return ChartSourceMetadata{}, fmt.Errorf("request to %s failed: %w", url, err)
	}
	if resp.StatusCode >= 300 {
		return ChartSourceMetadata{}, fmt.Errorf("request to %s returned response %q", url, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChartSourceMetadata{}, fmt.Errorf("failed to read response body: %w", err)
	}

	apiResp := ArtifactHubAPIHelm{}
	err = json.Unmarshal([]byte(body), &apiResp)
	if err != nil {
		return ChartSourceMetadata{}, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	if apiResp.ContentURL == "" {
		return ChartSourceMetadata{}, fmt.Errorf("ArtifactHub package: %s/%s not found", upstreamYaml.ArtifactHubRepo, upstreamYaml.ArtifactHubPackage)
	}

	upstreamYaml.HelmRepo = apiResp.Repository.URL
	upstreamYaml.HelmChart = apiResp.Name

	chartSourceMeta, err := fetchUpstreamHelmrepo(upstreamYaml)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	chartSourceMeta.Source = "ArtifactHub"

	return chartSourceMeta, nil
}

func getGitHubUserAndRepo(gitURL string) (string, string, error) {
	if !strings.HasPrefix(gitURL, "https://github.com") {
		err := fmt.Errorf("%s is not a GitHub URL", gitURL)
		return "", "", err
	}

	baseURL := strings.TrimPrefix(gitURL, "https://")
	baseURL = strings.TrimSuffix(baseURL, ".git")
	split := strings.Split(baseURL, "/")

	return split[1], split[2], nil
}

func fetchGitHubRelease(repoURL string) (string, error) {
	var releaseCommit string
	client := github.NewClient(nil)
	gitHubUser, gitHubRepo, err := getGitHubUserAndRepo(repoURL)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	opt := &github.ListOptions{Page: 1, PerPage: 50}
	latestRelease, _, err := client.Repositories.GetLatestRelease(ctx, gitHubUser, gitHubRepo)
	if err != nil {
		return "", err
	}
	for releaseCommit == "" {
		tags, _, _ := client.Repositories.ListTags(ctx, gitHubUser, gitHubRepo, opt)
		if len(tags) == 0 {
			break
		}
		opt.Page += 1
		for _, tag := range tags {
			if tag.GetName() == *latestRelease.TagName {
				releaseCommit = *tag.GetCommit().SHA
				break
			}
		}
	}

	if releaseCommit == "" {
		err = fmt.Errorf("commit not found for GitHub release")
		return "", err
	}

	logrus.Debugf("Fetching GitHub Release: %s (%s)\n", *latestRelease.Name, releaseCommit)

	return releaseCommit, nil
}

func gitCloneToDirectory(url, branch string, shallow bool) (string, error) {
	cloneOptions := git.CloneOptions{
		URL: url,
	}

	if shallow {
		cloneOptions.Depth = 1
	}

	if branch != "" {
		branchReference := fmt.Sprintf("refs/heads/%s", branch)
		cloneOptions.ReferenceName = plumbing.ReferenceName(branchReference)
	}

	tempDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		return "", err
	}

	_, err = git.PlainClone(tempDir, false, &cloneOptions)
	if err != nil {
		return "", err
	}

	return tempDir, nil
}

func gitCheckoutCommit(path, commit string) error {
	r, err := git.PlainOpen(path)
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	err = wt.Checkout(&git.CheckoutOptions{
		Hash: plumbing.NewHash(commit),
	})
	if err != nil {
		return err
	}

	return nil
}

// Constructs Chart Metadata for latest version published to Git Repository
func fetchUpstreamGit(upstreamYaml upstreamyaml.UpstreamYaml) (ChartSourceMetadata, error) {
	var upstreamCommit string

	clonePath, err := gitCloneToDirectory(upstreamYaml.GitRepo, upstreamYaml.GitBranch, !upstreamYaml.GitHubRelease)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	if upstreamYaml.GitHubRelease {
		logrus.Debug("Fetching GitHub Release")
		upstreamCommit, err = fetchGitHubRelease(upstreamYaml.GitRepo)
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		err = gitCheckoutCommit(clonePath, upstreamCommit)
		if err != nil {
			return ChartSourceMetadata{}, err
		}

	} else {
		r, err := git.PlainOpen(clonePath)
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		ref, err := r.Head()
		if err != nil {
			return ChartSourceMetadata{}, err
		}

		upstreamCommit = ref.Hash().String()
	}

	chartPath := clonePath
	if upstreamYaml.GitSubdirectory != "" {
		chartPath = filepath.Join(clonePath, upstreamYaml.GitSubdirectory)
		if _, err := os.Stat(chartPath); os.IsNotExist(err) {
			err = fmt.Errorf("git subdirectory '%s' does not exist", upstreamYaml.GitSubdirectory)
			return ChartSourceMetadata{}, err
		}
	}
	logrus.Debugf("Git Temp Directory: %s\n", chartPath)
	helmChart, err := loader.Load(chartPath)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	version := repo.ChartVersion{
		Metadata: helmChart.Metadata,
		URLs:     []string{upstreamYaml.GitRepo},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta := ChartSourceMetadata{
		Commit:       upstreamCommit,
		Source:       "Git",
		SubDirectory: upstreamYaml.GitSubdirectory,
		Versions:     versions,
	}

	err = os.RemoveAll(clonePath)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta, nil
}

func FetchUpstream(upstreamYaml upstreamyaml.UpstreamYaml) (ChartSourceMetadata, error) {
	var err error
	chartSourceMetadata := ChartSourceMetadata{}
	if upstreamYaml.ArtifactHubRepo != "" && upstreamYaml.ArtifactHubPackage != "" {
		chartSourceMetadata, err = fetchUpstreamArtifacthub(upstreamYaml)
	} else if upstreamYaml.HelmRepo != "" && upstreamYaml.HelmChart != "" {
		chartSourceMetadata, err = fetchUpstreamHelmrepo(upstreamYaml)
	} else if upstreamYaml.GitRepo != "" {
		chartSourceMetadata, err = fetchUpstreamGit(upstreamYaml)
	} else {
		err := errors.New("no valid repo options found")
		return ChartSourceMetadata{}, err
	}

	if upstreamYaml.ChartMetadata.Name != "" {
		for _, version := range chartSourceMetadata.Versions {
			version.Name = upstreamYaml.ChartMetadata.Name
		}
	}

	return chartSourceMetadata, err
}

func LoadChartFromURL(url string) (chart *chart.Chart, err error) {
	logrus.Debugf("Loading chart from %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		logrus.Errorf("Unable to fetch url %s", url)
		return nil, err
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	chart, err = loader.LoadArchive(resp.Body)
	if err != nil {
		logrus.Error(err)
		return nil, err
	}

	return chart, err
}

func LoadChartFromGit(url, subDirectory, commit string) (*chart.Chart, error) {
	clonePath, err := gitCloneToDirectory(url, "", false)
	if err != nil {
		return nil, err
	}

	err = gitCheckoutCommit(clonePath, commit)
	if err != nil {
		return nil, err
	}

	chartPath := clonePath
	if subDirectory != "" {
		chartPath = filepath.Join(clonePath, subDirectory)
		if _, err := os.Stat(chartPath); os.IsNotExist(err) {
			err = fmt.Errorf("git subdirectory '%s' does not exist", subDirectory)
			return nil, err
		}
	}

	helmChart, err := loader.Load(chartPath)
	if err != nil {
		return nil, err
	}

	err = os.RemoveAll(clonePath)

	return helmChart, err
}
