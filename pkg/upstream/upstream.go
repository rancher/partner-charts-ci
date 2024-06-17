package upstream

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rancher/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
)

// An Upstream is a place from which upstream charts are retrieved.
type Upstream interface {
	Fetch(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error)
}

type ChartSourceMetadata struct {
	Commit       string
	Source       string
	SubDirectory string
	Versions     repo.ChartVersions
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

func FetchUpstream(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	var upstream Upstream
	if upstreamYaml.AHRepoName != "" && upstreamYaml.AHPackageName != "" {
		upstream = ArtifactHubApiUpstream{}
	} else if upstreamYaml.HelmRepoUrl != "" && upstreamYaml.HelmChart != "" {
		upstream = HttpsHelmUpstream{}
	} else if upstreamYaml.GitRepoUrl != "" {
		upstream = GitHelmUpstream{}
	} else {
		return ChartSourceMetadata{}, errors.New("failed to get upstream")
	}

	chartSourceMetadata, err := upstream.Fetch(upstreamYaml)
	if err != nil {
		return ChartSourceMetadata{}, fmt.Errorf("failed to fetch data from upstream: %w", err)
	}

	if upstreamYaml.ChartYaml.Name != "" {
		for _, version := range chartSourceMetadata.Versions {
			version.Name = upstreamYaml.ChartYaml.Name
		}
	}

	return chartSourceMetadata, err
}

func LoadChartFromUrl(url string) (*chart.Chart, error) {
	logrus.Debugf("Loading chart from %s\n", url)
	resp, err := http.Get(url)
	if err != nil {
		logrus.Errorf("Unable to fetch url %s", url)
		return nil, err
	}

	defer resp.Body.Close()

	chart, err := loader.LoadArchive(resp.Body)
	if err != nil {
		logrus.Error(err)
		return nil, err
	}

	return chart, nil
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
