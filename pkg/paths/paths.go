package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func GetPaths() (Paths, error) {
	if paths == nil {
		// Check that we are at the root of a git repo
		repoRoot, err := os.Getwd()
		if err != nil {
			return Paths{}, fmt.Errorf("failed to get working directory: %w", err)
		}
		gitDirPath := filepath.Join(repoRoot, ".git")
		if fileInfo, err := os.Stat(gitDirPath); errors.Is(err, os.ErrNotExist) || !fileInfo.IsDir() {
			return Paths{}, errors.New("must be at the root of a git repo")
		} else if err != nil {
			return Paths{}, fmt.Errorf("failed to stat .git/: %w", err)
		}

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
	return *paths, nil
}
