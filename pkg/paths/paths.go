package paths

import (
	"os"

	"github.com/sirupsen/logrus"
)

// Fetches absolute repository root path
func GetRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}
