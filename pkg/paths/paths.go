package paths

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

var paths *Paths

// Paths is a type that contains all of the paths that are relevant
// to this tool. This approach facilitates unit testing.
type Paths struct {
	Assets            string
	Charts            string
	ConfigurationYaml string
	Icons             string
	IndexYaml         string
	Packages          string
	RepoRoot          string
}

func Get() Paths {
	if paths == nil {
		// TODO: once GetRepoRoot is no longer used, remove this call and inline
		// necessary parts of the code. We call it here in order to make sure
		// we are at the repository root.
		repoRoot := GetRepoRoot()
		assets := "assets"
		paths = &Paths{
			Assets:            assets,
			Charts:            "charts",
			ConfigurationYaml: "configuration.yaml",
			Icons:             filepath.Join(assets, "icons"),
			IndexYaml:         "index.yaml",
			Packages:          "packages",
			RepoRoot:          repoRoot,
		}
	}
	return *paths
}

// GetRepoRoot fetches absolute repository root path. If the working directory
// is not the root of a git repo, exits the program with an error.
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
