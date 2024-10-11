package validate

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/rancher/partner-charts-ci/pkg/conform"
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

func CloneRepo(url string, branch string, targetDir string) error {
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

func ChecksumFile(filePath string) (string, error) {
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

func CompareDirectories(upstreamPath, updatePath string) (DirectoryComparison, error) {
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
		relativePath := strings.TrimPrefix(upstreamFilePath, upstreamPath)
		checkedSet[relativePath] = checked

		if info.IsDir() {
			return nil
		}

		updateFilePath := path.Join(updatePath, relativePath)
		if _, err := os.Stat(updateFilePath); os.IsNotExist(err) {
			directoryComparison.Removed = append(directoryComparison.Removed, updateFilePath)
			return nil
		}
		leftCheckSum, err := ChecksumFile(upstreamFilePath)
		if err != nil {
			logrus.Error(err)
		}
		rightCheckSum, err := ChecksumFile(updateFilePath)
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
		relativePath := strings.TrimPrefix(updateFilePath, updatePath)

		if _, ok := checkedSet[relativePath]; !ok && !info.IsDir() {
			directoryComparison.Added = append(directoryComparison.Added, relativePath)
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

func matchHelmCharts(leftPath, rightPath string) (bool, error) {
	leftFile, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer leftFile.Close()

	rightFile, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer rightFile.Close()

	leftChart, err := loader.LoadArchive(leftFile)
	if err != nil {
		return false, err
	}

	rightChart, err := loader.LoadArchive(rightFile)
	if err != nil {
		return false, err
	}

	for annotation := range leftChart.Metadata.Annotations {
		if strings.HasPrefix(annotation, "catalog.cattle.io") {
			delete(leftChart.Metadata.Annotations, annotation)
		}
	}

	for annotation := range rightChart.Metadata.Annotations {
		if strings.HasPrefix(annotation, "catalog.cattle.io") {
			delete(rightChart.Metadata.Annotations, annotation)
		}
	}

	tempDir, err := os.MkdirTemp(os.TempDir(), "chartValidate")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tempDir)

	leftOut := path.Join(tempDir, "left")
	rightOut := path.Join(tempDir, "right")

	err = conform.ExportChartDirectory(leftChart, leftOut)
	if err != nil {
		return false, err
	}

	err = conform.ExportChartDirectory(rightChart, rightOut)
	if err != nil {
		return false, err
	}

	directoryComparison, err := CompareDirectories(leftOut, rightOut)

	return directoryComparison.Match(), err
}
