package paths

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// GetRepoRoot fetches absolute repository root path. If the
// working directory is not the root of a git repo, exits the
// program with an error.
func GetRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatalf("failed to get working directory: %s", err)
	}

	gitDirPath := filepath.Join(repoRoot, ".git")
	if fileInfo, err := os.Stat(gitDirPath); err == nil && fileInfo.IsDir() {
		return repoRoot
	} else if errors.Is(err, os.ErrNotExist) || !fileInfo.IsDir() {
		logrus.Fatalf("must be at the root of a git repo")
	}

	logrus.Fatalf("failed to check whether working directory is root of git repo: %s", err)
	return ""
}
