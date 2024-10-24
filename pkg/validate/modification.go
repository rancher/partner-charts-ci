package validate

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rancher/partner-charts-ci/pkg/conform"
	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/chart/loader"
)

type DirectoryComparison struct {
	Unchanged []string
	Modified  []string
	Added     []string
	Removed   []string
}

func (directoryComparison *DirectoryComparison) Match() bool {
	return len(directoryComparison.Modified)+len(directoryComparison.Added)+len(directoryComparison.Removed) == 0
}

func (directoryComparison *DirectoryComparison) Merge(newComparison DirectoryComparison) {
	directoryComparison.Unchanged = append(directoryComparison.Unchanged, newComparison.Unchanged...)
	directoryComparison.Modified = append(directoryComparison.Modified, newComparison.Modified...)
	directoryComparison.Added = append(directoryComparison.Added, newComparison.Added...)
	directoryComparison.Removed = append(directoryComparison.Removed, newComparison.Removed...)
}

// preventReleasedChartModifications validates that no released chart
// versions have been modified outside of a few that must be allowed,
// such as the deprecated field of Chart.yaml and Rancher-specific
// annotations.
func preventReleasedChartModifications(paths p.Paths, configYaml ConfigurationYaml) []error {
	cloneDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		logrus.Fatal(err)
	}
	defer os.RemoveAll(cloneDir)

	err = cloneRepo(configYaml.ValidateUpstreams[0].Url, configYaml.ValidateUpstreams[0].Branch, cloneDir)
	if err != nil {
		logrus.Fatal(err)
	}

	directoryComparison := DirectoryComparison{}
	for _, dirPath := range []string{"assets"} {
		upstreamPath := filepath.Join(cloneDir, dirPath)
		// TODO: leaving this (almost) as-is because this was changed in #35.
		// Use paths.Assets instead of paths.RepoRoot once that PR is merged.
		updatePath := filepath.Join(paths.RepoRoot, dirPath)
		if _, err := os.Stat(updatePath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in source. Skipping...", dirPath)
			continue
		}
		if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in upstream. Skipping...", dirPath)
			continue
		}
		newComparison, err := compareDirectories(upstreamPath, updatePath, []string{"icons"})
		if err != nil {
			logrus.Error(err)
		}
		directoryComparison.Merge(newComparison)
	}

	errors := make([]error, 0, len(directoryComparison.Modified))
	for _, modifiedFile := range directoryComparison.Modified {
		errors = append(errors, fmt.Errorf("%s was modified", modifiedFile))
	}
	return errors
}

func cloneRepo(url string, branch string, targetDir string) error {
	branchReference := fmt.Sprintf("refs/heads/%s", branch)
	cloneOptions := git.CloneOptions{
		URL:           url,
		ReferenceName: plumbing.ReferenceName(branchReference),
		SingleBranch:  true,
		Depth:         1,
	}

	_, err := git.PlainClone(targetDir, false, &cloneOptions)
	if err != nil {
		return err
	}

	return nil
}

func checksumFile(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	hash := fmt.Sprintf("%x", h.Sum(nil))

	return hash, nil
}

func compareDirectories(upstreamPath, updatePath string, skipDirs []string) (DirectoryComparison, error) {
	logrus.Debugf("Comparing directories %s and %s", upstreamPath, updatePath)
	directoryComparison := DirectoryComparison{}
	checkedSet := make(map[string]struct{})
	var checked = struct{}{}

	if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
		return directoryComparison, err
	}
	if _, err := os.Stat(updatePath); os.IsNotExist(err) {
		return directoryComparison, err
	}

	findRemovalAndModification := func(upstreamFilePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(upstreamPath, upstreamFilePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path of %s: %w", upstreamFilePath, err)
		}
		checkedSet[relativePath] = checked

		if info.IsDir() {
			if slices.Contains(skipDirs, relativePath) {
				return filepath.SkipDir
			}
			return nil
		}

		updateFilePath := filepath.Join(updatePath, relativePath)
		if _, err := os.Stat(updateFilePath); os.IsNotExist(err) {
			directoryComparison.Removed = append(directoryComparison.Removed, updateFilePath)
			return nil
		}
		leftCheckSum, err := checksumFile(upstreamFilePath)
		if err != nil {
			logrus.Error(err)
		}
		rightCheckSum, err := checksumFile(updateFilePath)
		if err != nil {
			logrus.Error(err)
		}

		if leftCheckSum != rightCheckSum && strings.HasSuffix(upstreamFilePath, ".tgz") {
			chartMatch, err := matchHelmCharts(upstreamFilePath, updateFilePath)
			if chartMatch {
				directoryComparison.Unchanged = append(directoryComparison.Unchanged, updateFilePath)
			} else {
				directoryComparison.Modified = append(directoryComparison.Modified, updateFilePath)
			}
			if err != nil {
				logrus.Debug(err)
			}
		} else if leftCheckSum != rightCheckSum {
			directoryComparison.Modified = append(directoryComparison.Modified, updateFilePath)
		} else {
			directoryComparison.Unchanged = append(directoryComparison.Unchanged, updateFilePath)
		}

		return nil
	}

	findAddition := func(updateFilePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(updatePath, updateFilePath)
		if err != nil {
			return fmt.Errorf("failed to get relative path of %s: %w", updateFilePath, err)
		}

		if info.IsDir() {
			if slices.Contains(skipDirs, relativePath) {
				return filepath.SkipDir
			}
			return nil
		}

		if _, ok := checkedSet[relativePath]; !ok {
			directoryComparison.Added = append(directoryComparison.Added, updateFilePath)
		}

		return nil
	}

	if err := filepath.Walk(upstreamPath, findRemovalAndModification); err != nil {
		return DirectoryComparison{}, fmt.Errorf("failed to search %q for removed or modified files: %w", upstreamPath, err)
	}
	if err := filepath.Walk(updatePath, findAddition); err != nil {
		return DirectoryComparison{}, fmt.Errorf("failed to search %q for added files: %w", updatePath, err)
	}

	return directoryComparison, nil
}

// prepareTgzForComparison takes a path to a .tgz helm chart. It unpacks this
// helm chart, applies modifications to it that cause the validation process
// to ignore certain changes, and exports it to a temporary chart directory.
// The caller is responsible for removing the chart directory after they are
// finished using it.
func prepareTgzForComparison(tgzPath string) (string, error) {
	upstreamFile, err := os.Open(tgzPath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer upstreamFile.Close()

	upstreamChart, err := loader.LoadArchive(upstreamFile)
	if err != nil {
		return "", fmt.Errorf("failed to load archive: %w", err)
	}

	// Do not consider changes to partner-charts-specific chart annotations
	for annotation := range upstreamChart.Metadata.Annotations {
		if strings.HasPrefix(annotation, "catalog.cattle.io") {
			delete(upstreamChart.Metadata.Annotations, annotation)
		}
	}

	// Do not consider changes to the Chart.yaml deprecated field
	upstreamChart.Metadata.Deprecated = false

	chartDirectory, err := os.MkdirTemp("", "partner-charts-ci-validate-")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}

	if err = conform.ExportChartDirectory(upstreamChart, chartDirectory); err != nil {
		return "", fmt.Errorf("failed to export chart directory: %w", err)
	}

	return chartDirectory, nil
}

func matchHelmCharts(upstreamPath, updatePath string) (bool, error) {
	upstreamChartDirectory, err := prepareTgzForComparison(upstreamPath)
	if err != nil {
		return false, fmt.Errorf("failed to prepare %s for comparison: %w", upstreamPath, err)
	}
	defer os.RemoveAll(upstreamChartDirectory)

	updateChartDirectory, err := prepareTgzForComparison(updatePath)
	if err != nil {
		return false, fmt.Errorf("failed to prepare %s for comparison: %w", updatePath, err)
	}
	defer os.RemoveAll(updateChartDirectory)

	directoryComparison, err := compareDirectories(upstreamChartDirectory, updateChartDirectory, []string{})
	if err != nil {
		return false, fmt.Errorf("failed to compare directories: %w", err)
	}

	return directoryComparison.Match(), err
}
