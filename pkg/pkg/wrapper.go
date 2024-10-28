package pkg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/rancher/partner-charts-ci/pkg/conform"
	"github.com/rancher/partner-charts-ci/pkg/fetcher"
	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/rancher/partner-charts-ci/pkg/upstreamyaml"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/repo"
)

// PackageWrapper is the manifestation of the concept of a package,
// which is configuration that refers to an upstream helm chart plus
// any local modifications that may be made to those helm charts as
// they are being integrated into the partner charts repository.
//
// PackageWrapper is not called Package because the most obvious name
// for instances of it would be "package", which conflicts with the
// "package" golang keyword.
type PackageWrapper struct {
	// The developer-facing name of the chart
	Name string
	// The user-facing (i.e. pretty) chart name
	DisplayName string
	// Filtered subset of versions to be fetched
	FetchVersions repo.ChartVersions
	// Path stores the package path in current repository
	Path string
	// SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	// The package's upstream.yaml file
	UpstreamYaml *upstreamyaml.UpstreamYaml
	// The user-facing (i.e. pretty) chart vendor name
	DisplayVendor string
	// The developer-facing chart vendor name
	Vendor string
}

type PackageList []PackageWrapper

func (p PackageList) Len() int {
	return len(p)
}

func (p PackageList) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func (p PackageList) Less(i, j int) bool {
	if p[i].SourceMetadata != nil && p[j].SourceMetadata != nil {
		if p[i].Vendor != p[j].Vendor {
			return p[i].Vendor < p[j].Vendor
		}
		return p[i].Name < p[j].Name
	}

	return false
}

func (packageWrapper *PackageWrapper) FullName() string {
	return packageWrapper.Vendor + "/" + packageWrapper.Name
}

// Populates PackageWrapper with relevant data from upstream and
// checks for updates. Returns true if newer package version is
// available.
func (packageWrapper *PackageWrapper) Populate(paths p.Paths) (bool, error) {
	sourceMetadata, err := fetcher.FetchUpstream(*packageWrapper.UpstreamYaml)
	if err != nil {
		return false, fmt.Errorf("failed to fetch data from upstream: %w", err)
	}
	if sourceMetadata.Versions[0].Name != packageWrapper.Name {
		logrus.Warnf("upstream name %q does not match package name %q", sourceMetadata.Versions[0].Name, packageWrapper.Name)
	}
	packageWrapper.SourceMetadata = &sourceMetadata

	packageWrapper.FetchVersions, err = filterVersions(
		paths,
		packageWrapper.SourceMetadata.Versions,
		packageWrapper.UpstreamYaml.Fetch,
	)
	if err != nil {
		return false, err
	}

	if len(packageWrapper.FetchVersions) == 0 {
		return false, nil
	}

	return true, nil
}

// GetOverlayFiles returns the package's overlay files as a map where
// the keys are the path to the file relative to the helm chart root
// (i.e. Chart.yaml would have the path "Chart.yaml") and the values
// are the contents of the file.
func (pw PackageWrapper) GetOverlayFiles() (map[string][]byte, error) {
	overlayFiles := map[string][]byte{}
	overlayDir := filepath.Join(pw.Path, "overlay")
	err := filepath.WalkDir(overlayDir, func(path string, dirEntry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return fs.SkipAll
		} else if err != nil {
			return fmt.Errorf("error related to %q: %w", path, err)
		}
		if dirEntry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %q: %w", path, err)
		}
		relativePath, err := filepath.Rel(overlayDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}
		overlayFiles[relativePath] = contents
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk files: %w", err)
	}
	return overlayFiles, nil
}

// ListPackageWrappers reads packages and their upstream.yaml from the packages
// directory and returns them in a slice. If currentPackage is specified,
// it must be in <vendor>/<name> format (i.e. the "full" package name).
// If currentPackage is specified, the function returns a slice with only
// one element, which is the specified package.
func ListPackageWrappers(paths p.Paths, currentPackage string) (PackageList, error) {
	var globPattern string
	if currentPackage == "" {
		globPattern = paths.Packages + "/*/*"
	} else {
		globPattern = filepath.Join(paths.Packages, currentPackage)
	}
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob for packages")
	}
	if currentPackage != "" {
		if len(matches) == 0 {
			return nil, fmt.Errorf("failed to find package %q", currentPackage)
		} else if length := len(matches); length > 1 {
			return nil, fmt.Errorf("found %d packages for %q, expected 1", length, currentPackage)
		}
	}

	packageList := make(PackageList, 0, len(matches))
	for _, match := range matches {
		parts := strings.Split(match, "/")
		if len(parts) != 3 {
			return nil, fmt.Errorf("failed to split %q into 3 parts", match)
		}
		packageWrapper := PackageWrapper{
			Path:   match,
			Vendor: parts[1],
			Name:   parts[2],
		}

		upstreamYamlPath := filepath.Join(packageWrapper.Path, "upstream.yaml")
		upstreamYaml, err := upstreamyaml.Parse(upstreamYamlPath)
		if err != nil {
			return nil, fmt.Errorf("failed to parse upstream.yaml: %w", err)
		}
		packageWrapper.UpstreamYaml = upstreamYaml

		if packageWrapper.UpstreamYaml.Vendor != "" {
			packageWrapper.DisplayVendor = packageWrapper.UpstreamYaml.Vendor
		} else {
			packageWrapper.DisplayVendor = packageWrapper.Vendor
		}

		if packageWrapper.UpstreamYaml.DisplayName != "" {
			packageWrapper.DisplayName = packageWrapper.UpstreamYaml.DisplayName
		} else {
			packageWrapper.DisplayName = packageWrapper.Name
		}

		packageList = append(packageList, packageWrapper)
	}

	return packageList, nil
}

func filterVersions(paths p.Paths, upstreamVersions repo.ChartVersions, fetch string) (repo.ChartVersions, error) {
	logrus.Debugf("Filtering versions for %s\n", upstreamVersions[0].Name)
	upstreamVersions = stripPreRelease(upstreamVersions)
	if len(upstreamVersions) == 0 {
		err := fmt.Errorf("No versions available in upstream or all versions are marked pre-release")
		return repo.ChartVersions{}, err
	}
	allStoredVersions, err := getStoredVersions(paths, upstreamVersions[0].Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get stored versions: %w", err)
	}
	filteredVersions := collectNonStoredVersions(upstreamVersions, allStoredVersions, fetch)

	return filteredVersions, nil
}

func stripPreRelease(versions repo.ChartVersions) repo.ChartVersions {
	strippedVersions := make(repo.ChartVersions, 0)
	for _, version := range versions {
		semVer, err := semver.NewVersion(version.Version)
		if err != nil {
			logrus.Error(err)
			continue
		}
		if semVer.Prerelease() == "" {
			strippedVersions = append(strippedVersions, version)
		}
	}

	return strippedVersions
}

func collectNonStoredVersions(versions repo.ChartVersions, storedVersions repo.ChartVersions, fetch string) repo.ChartVersions {
	nonStoredVersions := make(repo.ChartVersions, 0)
	for i, version := range versions {
		parsedVersion, err := semver.NewVersion(version.Version)
		if err != nil {
			logrus.Error(err)
		}
		stored := false
		logrus.Debugf("Checking if version %s is stored\n", version.Version)
		for _, storedVersion := range storedVersions {
			strippedStoredVersion := conform.StripPackageVersion(storedVersion.Version)
			if storedVersion.Version == parsedVersion.String() {
				logrus.Debugf("Found version %s\n", storedVersion.Version)
				stored = true
				break
			} else if strippedStoredVersion == parsedVersion.String() {
				logrus.Debugf("Found modified version %s\n", storedVersion.Version)
				stored = true
				break
			}
		}
		if stored && i == 0 && (strings.ToLower(fetch) == "" || strings.ToLower(fetch) == "latest") {
			logrus.Debugf("Latest version already stored")
			break
		}
		if !stored {
			if fetch == strings.ToLower("newer") {
				var semVer *semver.Version
				semVer, err := semver.NewVersion(version.Version)
				if err != nil {
					logrus.Error(err)
					continue
				}
				if len(storedVersions) > 0 {
					strippedStoredLatest := conform.StripPackageVersion(storedVersions[0].Version)
					storedLatestSemVer, err := semver.NewVersion(strippedStoredLatest)
					if err != nil {
						logrus.Error(err)
						continue
					}
					if semVer.GreaterThan(storedLatestSemVer) {
						logrus.Debugf("Version: %s > %s\n", semVer.String(), storedVersions[0].Version)
						nonStoredVersions = append(nonStoredVersions, version)
					}
				} else {
					nonStoredVersions = append(nonStoredVersions, version)
				}
			} else if fetch == strings.ToLower("all") {
				nonStoredVersions = append(nonStoredVersions, version)
			} else {
				nonStoredVersions = append(nonStoredVersions, version)
				break
			}
		}
	}

	return nonStoredVersions
}

func getStoredVersions(paths p.Paths, chartName string) (repo.ChartVersions, error) {
	storedVersions := repo.ChartVersions{}
	helmIndexYaml, err := repo.LoadIndexFile(paths.IndexYaml)
	if err != nil {
		return storedVersions, fmt.Errorf("failed to load index file: %w", err)
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		storedVersions = append(storedVersions, val...)
	}

	return storedVersions, nil
}
