package validate

import (
	"fmt"
	"os"
	"path/filepath"

	p "github.com/rancher/partner-charts-ci/pkg/paths"
)

func validatePackagesDirectory(paths p.Paths, _ ConfigurationYaml) []error {
	errors := make([]error, 0)

	// packages/ and packages/<vendor> may contain only directories
	globPatterns := [2]string{paths.Packages + "/*", paths.Packages + "/*/*"}
	for _, globPattern := range globPatterns {
		matches, err := filepath.Glob(globPattern)
		if err != nil {
			error := fmt.Errorf("failed to run glob pattern %q: %w", globPattern, err)
			errors = append(errors, error)
			continue
		}
		for _, match := range matches {
			fileInfo, err := os.Stat(match)
			if err != nil {
				error := fmt.Errorf("failed to stat %s: %w", match, err)
				errors = append(errors, error)
				continue
			}
			if !fileInfo.IsDir() {
				error := fmt.Errorf("%s may contain only directories, but %s is not a directory", filepath.Dir(match), match)
				errors = append(errors, error)
			}
		}
	}

	// packages/<vendor>/<name> may contain only upstream.yaml file or overlay directory
	globPattern := paths.Packages + "/*/*/*"
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		error := fmt.Errorf("failed to run glob pattern %q: %w", globPattern, err)
		errors = append(errors, error)
		return errors
	}
	for _, match := range matches {
		fileInfo, err := os.Stat(match)
		if err != nil {
			error := fmt.Errorf("failed to stat %s: %w", match, err)
			errors = append(errors, error)
			continue
		}

		baseName := filepath.Base(match)
		switch baseName {
		case "upstream.yaml":
			if fileInfo.IsDir() {
				error := fmt.Errorf("%s must be a file", match)
				errors = append(errors, error)
			}
		case "overlay":
			if !fileInfo.IsDir() {
				error := fmt.Errorf("%s must be a directory", match)
				errors = append(errors, error)
			}
		default:
			error := fmt.Errorf("only upstream.yaml and overlay directory may exist in package directories but found %s", match)
			errors = append(errors, error)
		}
	}

	return errors
}
