package icons

import (
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/repo"
)

const (
	partnerFilePath     = "index.yaml"
	partnerDownloadPath = "assets/logos"
)

// PackageIconOverride is a struct to hold the package name, path, and icon to override
type PackageIconOverride struct {
	Name string
	Path string
	Icon string
}

// PackageIconList is a list of PackageIconOverride
type PackageIconList []PackageIconOverride

// PackageIconMap is a map of PackageIconOverride
type PackageIconMap map[string]PackageIconOverride

// ParsePackageToOverride will convert a Package from a PackageList to a PackageIcon for a PackageIconList
func ParsePackageToOverride(name, path, icon string) PackageIconOverride {
	return PackageIconOverride{
		Name: name,
		Path: path,
		Icon: icon,
	}
}

// CheckFilesStructure will check if index.yaml and assets/logos exist
// will log and exit if the file structure does not exist
func CheckFilesStructure() {
	if _, err := os.Stat(partnerFilePath); os.IsNotExist(err) {
		fmt.Printf("File not found: %s\n", partnerFilePath)
		logrus.Fatalf("File not found: %s\n", partnerFilePath)
	}
	_, err := os.Stat(partnerDownloadPath)
	if os.IsNotExist(err) {
		logrus.Fatalf("Directory not found: %s\n", partnerDownloadPath)
	}
}

// CheckForOverrideConditions will check if the package has an upstream.yaml file and if the iconURL is a local file
func CheckForOverrideConditions(packagePath string, iconURL string) bool {
	var err error
	if os.Stat(fmt.Sprintf("%s/upstream.yaml", packagePath)); os.IsNotExist(err) {
		return false
	}
	if iconURL != "" {
		if strings.HasPrefix(iconURL, "file://assets/logos") {
			return true
		}
	}
	return false
}

// ConvertYamlForIconOverride will change the metade icon URL to a local icon path for the index.yaml
func ConvertYamlForIconOverride(helmIndexYaml *repo.IndexFile, packageIconList PackageIconList) {
	for _, pkg := range packageIconList {
		for _, pk := range helmIndexYaml.Entries[pkg.Name] {
			pk.Metadata.Icon = pkg.Icon
		}
	}
}

// TestIconsAndIndexYaml will if Icons and IndexYaml have matching paths
func TestIconsAndIndexYaml(packageIconList PackageIconList, helmIndexFile *repo.IndexFile) error {
	var err error
	for _, pkg := range packageIconList {
		// Check if the icon has the correct path prefix
		if !strings.HasPrefix(pkg.Icon, "file://") {
			continue // Skip if the icon is not a local file
		}
		iconPath := strings.TrimPrefix(pkg.Icon, "file://")
		// Check if icon exists in the path
		_, openIconErr := os.Stat(iconPath)
		if os.IsNotExist(openIconErr) {
			logrus.Errorf("Icon %s not found", pkg.Icon)
			err = fmt.Errorf("icon (%s) not found - TestIconsAndIndexYaml", iconPath)
		}
		// Check if the icon path is the same as the index.yaml
		ok := helmIndexFile.Entries[pkg.Name][0].Metadata.Icon == pkg.Icon
		if !ok {
			logrus.Errorf("Package %s not found in index.yaml", pkg.Name)
			err = fmt.Errorf("icon (%s) differ from index.yaml - TestIconsAndIndexYaml", pkg.Icon)
		}

	}

	if err != nil {
		return err
	}

	// Count present logos and logo entries in index yaml
	presentLogoFiles, err := countLogoFilesInAssets()
	presentLogoEntries := countLogoFilesInIndex(helmIndexFile)
	if err != nil {
		logrus.Errorf("Error reading assets/logos: %v", err)
		return err
	}
	if presentLogoFiles != presentLogoEntries {
		logrus.Errorf("Logo files in assets/logos: %d, Logo files in index.yaml: %d", presentLogoFiles, presentLogoEntries)
		return fmt.Errorf("logo files in assets/logos and index.yaml do not match - TestIconsAndIndexYaml")
	}
	return err
}

func countLogoFilesInAssets() (int, error) {
	files, err := os.ReadDir("assets/logos")
	if err != nil {
		return 0, err
	}

	count := 0
	for _, file := range files {
		if !file.IsDir() {
			count++
		}
	}

	return count, nil
}

func countLogoFilesInIndex(helmIndexFile *repo.IndexFile) int {
	var counter int
	for _, entry := range helmIndexFile.Entries {
		icon := entry[0].Metadata.Icon
		if strings.HasPrefix(icon, "file://") {
			counter++
		} else {
			logrus.Warnf("Icon %s of entry %s is not a local file", icon, entry[0].Name)
		}
	}
	return counter
}
