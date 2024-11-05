package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	p "github.com/rancher/partner-charts-ci/pkg/paths"

	"helm.sh/helm/v3/pkg/repo"
)

// Checks two things related to icons: that the icon field of each chart version
// in index.yaml refers to an icon that exists in the icons directory, and
// that every icon in the icons directory is referred to by a chart version
// in index.yaml.
func validateIcons(paths p.Paths, _ ConfigurationYaml) []error {

	indexYaml, err := repo.LoadIndexFile(paths.IndexYaml)
	if err != nil {
		return []error{fmt.Errorf("failed to load index.yaml: %w", err)}
	}

	dirEntries, err := os.ReadDir(paths.Icons)
	if err != nil {
		return []error{fmt.Errorf("failed to read icons directory: %w", err)}
	}

	iconFiles := make([]string, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		iconPath := filepath.Join("assets", "icons", dirEntry.Name())
		iconFiles = append(iconFiles, iconPath)
	}

	return validateLoadedIcons(indexYaml, iconFiles)
}

func validateLoadedIcons(indexYaml *repo.IndexFile, iconFiles []string) []error {
	errors := make([]error, 0, len(iconFiles))

	iconMap := make(map[string]bool)
	for _, icon := range iconFiles {
		iconMap[icon] = false
	}

	// check that every icon referenced in index.yaml exists in icon directory
	for _, chartVersions := range indexYaml.Entries {
		for _, chartVersion := range chartVersions {
			filePath := strings.TrimPrefix(chartVersion.Icon, "file://")
			if _, ok := iconMap[filePath]; !ok {
				error := fmt.Errorf("icon file %s for %s version %s does not exist", filePath, chartVersion.Name, chartVersion.Version)
				errors = append(errors, error)
			} else {
				iconMap[filePath] = true
			}
		}
	}

	// check that every icon in icon directory is referenced in index.yaml
	for filePath, present := range iconMap {
		if !present {
			error := fmt.Errorf("icon file %s is not referenced in index.yaml", filePath)
			errors = append(errors, error)
		}
	}

	return errors
}
