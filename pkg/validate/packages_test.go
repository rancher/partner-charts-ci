package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/stretchr/testify/assert"
)

func getPaths(t *testing.T) p.Paths {
	t.Helper()
	repoRoot := t.TempDir()
	return p.Paths{
		Packages: filepath.Join(repoRoot, "packages"),
	}
}

// Creates and populates a package directory with an upstream.yaml file
// and an overlay directory. Returns the path to the package directory.
func createPackageDirectory(t *testing.T, paths p.Paths) string {
	t.Helper()
	packageDir := filepath.Join(paths.Packages, "testVendor", "testPackage")
	overlayDir := filepath.Join(packageDir, "overlay")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatalf("failed to create %s: %s", overlayDir, err)
	}
	upstreamYamlFile := filepath.Join(packageDir, "upstream.yaml")
	if err := os.WriteFile(upstreamYamlFile, []byte("fake upstream.yaml"), 0o644); err != nil {
		t.Fatalf("failed to write %s: %s", upstreamYamlFile, err)
	}
	return packageDir
}

func TestValidatePackagesDirectory(t *testing.T) {
	for _, dirName := range [2]string{
		"",
		"testVendor",
	} {
		paths := getPaths(t)
		testDirectory := filepath.Join(paths.Packages, dirName)
		t.Run(fmt.Sprintf("should return an error when a file is in %s", testDirectory), func(t *testing.T) {
			goodDirectory := filepath.Join(testDirectory, "goodDirectory")
			if err := os.MkdirAll(goodDirectory, 0o755); err != nil {
				t.Fatalf("unexpected error creating %s: %s", goodDirectory, err)
			}
			badFile := filepath.Join(testDirectory, "badFile")
			if err := os.WriteFile(badFile, []byte("badFile contents"), 0o644); err != nil {
				t.Fatalf("failed to write %s: %s", badFile, err)
			}
			errors := validatePackagesDirectory(paths, ConfigurationYaml{})
			assert.Len(t, errors, 1)
			assert.ErrorContains(t, errors[0], fmt.Sprintf("may contain only directories, but %s is not a directory", badFile))
		})
	}

	t.Run("should return an error when a file that is not upstream.yaml is in packages/vendor/packageName", func(t *testing.T) {
		paths := getPaths(t)
		packageDirectory := createPackageDirectory(t, paths)
		badFile := filepath.Join(packageDirectory, "badFile")
		if err := os.WriteFile(badFile, []byte("badFile contents"), 0o644); err != nil {
			t.Fatalf("failed to write %s: %s", badFile, err)
		}
		errors := validatePackagesDirectory(paths, ConfigurationYaml{})
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], fmt.Sprintf("only upstream.yaml and overlay directory may exist in package directories but found %s", badFile))
	})

	t.Run("should return an error when a dir that is not overlay is in packages/vendor/packageName", func(t *testing.T) {
		paths := getPaths(t)
		packageDirectory := createPackageDirectory(t, paths)
		badDir := filepath.Join(packageDirectory, "badFile")
		if err := os.MkdirAll(badDir, 0o755); err != nil {
			t.Fatalf("failed to create %s: %s", badDir, err)
		}
		errors := validatePackagesDirectory(paths, ConfigurationYaml{})
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], fmt.Sprintf("only upstream.yaml and overlay directory may exist in package directories but found %s", badDir))
	})
}
