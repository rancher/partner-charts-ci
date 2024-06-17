package upstream

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/google/go-github/v53/github"
	"github.com/rancher/partner-charts-ci/pkg/parse"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
)

// Constructs Chart Metadata for latest version published to Git Repository
func fetchUpstreamGit(upstreamYaml parse.UpstreamYaml) (ChartSourceMetadata, error) {
	var upstreamCommit string

	clonePath, err := gitCloneToDirectory(upstreamYaml.GitRepoUrl, upstreamYaml.GitBranch, !upstreamYaml.GitHubRelease)
	if err != nil {
		return ChartSourceMetadata{}, err
	}

	if upstreamYaml.GitHubRelease {
		logrus.Debug("Fetching GitHub Release")
		upstreamCommit, err = fetchGitHubRelease(upstreamYaml.GitRepoUrl)
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
	if upstreamYaml.GitSubDirectory != "" {
		chartPath = filepath.Join(clonePath, upstreamYaml.GitSubDirectory)
		if _, err := os.Stat(chartPath); os.IsNotExist(err) {
			err = fmt.Errorf("git subdirectory '%s' does not exist", upstreamYaml.GitSubDirectory)
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
		URLs:     []string{upstreamYaml.GitRepoUrl},
	}

	versions := repo.ChartVersions{&version}

	chartSourceMeta := ChartSourceMetadata{
		Commit:       upstreamCommit,
		Source:       "Git",
		SubDirectory: upstreamYaml.GitSubDirectory,
		Versions:     versions,
	}

	err = os.RemoveAll(clonePath)
	if err != nil {
		logrus.Debug(err)
	}

	return chartSourceMeta, nil
}

func getGitHubUserAndRepo(gitUrl string) (string, string, error) {
	if !strings.HasPrefix(gitUrl, "https://github.com") {
		err := fmt.Errorf("%s is not a GitHub URL", gitUrl)
		return "", "", err
	}

	baseUrl := strings.TrimPrefix(gitUrl, "https://")
	baseUrl = strings.TrimSuffix(baseUrl, ".git")
	split := strings.Split(baseUrl, "/")

	return split[1], split[2], nil

}

func fetchGitHubRelease(repoUrl string) (string, error) {
	var releaseCommit string
	client := github.NewClient(nil)
	gitHubUser, gitHubRepo, err := getGitHubUserAndRepo(repoUrl)
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
		err = fmt.Errorf("Commit not found for GitHub release")
		return "", err
	}

	logrus.Debugf("Fetching GitHub Release: %s (%s)\n", *latestRelease.Name, releaseCommit)

	return releaseCommit, nil
}
