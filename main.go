package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/rancher/partner-charts-ci/pkg/conform"
	"github.com/rancher/partner-charts-ci/pkg/fetcher"
	"github.com/rancher/partner-charts-ci/pkg/icons"
	"github.com/rancher/partner-charts-ci/pkg/parse"
	"github.com/rancher/partner-charts-ci/pkg/validate"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/repo"
)

const (
	annotationAutoInstall  = "catalog.cattle.io/auto-install"
	annotationCertified    = "catalog.cattle.io/certified"
	annotationDisplayName  = "catalog.cattle.io/display-name"
	annotationExperimental = "catalog.cattle.io/experimental"
	annotationFeatured     = "catalog.cattle.io/featured"
	annotationHidden       = "catalog.cattle.io/hidden"
	annotationKubeVersion  = "catalog.cattle.io/kube-version"
	annotationNamespace    = "catalog.cattle.io/namespace"
	annotationReleaseName  = "catalog.cattle.io/release-name"
	//indexFile sets the filename for the repo index yaml
	indexFile = "index.yaml"
	//packageEnvVariable sets the environment variable to check for a package name
	packageEnvVariable = "PACKAGE"
	//repositoryAssetsDir sets the directory name for chart asset files
	repositoryAssetsDir = "assets"
	//repositoryChartsDir sets the directory name for stored charts
	repositoryChartsDir = "charts"
	//repositoryPackagesDir sets the directory name for package configurations
	repositoryPackagesDir = "packages"
	configOptionsFile     = "configuration.yaml"
	featuredMax           = 5
)

var (
	version = "v0.0.0"
	commit  = "HEAD"
)

// ChartWrapper is like a chart.Chart, but it tracks whether the chart
// has been modified so that we can avoid making changes to chart
// artifacts when the chart has not been modified.
type ChartWrapper struct {
	*chart.Chart
	Modified bool
}

func NewChartWrapper(helmChart *chart.Chart) *ChartWrapper {
	return &ChartWrapper{
		Chart:    helmChart,
		Modified: false,
	}
}

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
	// LatestStored stores the latest version of the chart currently in the repo
	LatestStored repo.ChartVersion
	// SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	// The package's upstream.yaml file
	UpstreamYaml *parse.UpstreamYaml
	// The user-facing (i.e. pretty) chart vendor name
	Vendor string
	// The developer-facing chart vendor name
	ParsedVendor string
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
		if p[i].ParsedVendor != p[j].ParsedVendor {
			return p[i].ParsedVendor < p[j].ParsedVendor
		}
		return p[i].Name < p[j].Name
	}

	return false
}

// Populates PackageWrapper with relevant data from upstream and
// checks for updates. If onlyLatest is true, then it puts only the
// latest upstream chart version in PackageWrapper.FetchVersions.
// Returns true if newer package version is available.
func (packageWrapper *PackageWrapper) populate(onlyLatest bool) (bool, error) {
	sourceMetadata, err := generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
	if err != nil {
		return false, err
	}
	if sourceMetadata.Versions[0].Name != packageWrapper.Name {
		logrus.Warnf("upstream name %q does not match package name %q", sourceMetadata.Versions[0].Name, packageWrapper.Name)
	}
	packageWrapper.SourceMetadata = sourceMetadata

	if onlyLatest {
		packageWrapper.UpstreamYaml.Fetch = "latest"
		if packageWrapper.UpstreamYaml.TrackVersions != nil {
			packageWrapper.UpstreamYaml.TrackVersions = []string{packageWrapper.UpstreamYaml.TrackVersions[0]}
		}
	}

	packageWrapper.FetchVersions, err = filterVersions(
		packageWrapper.SourceMetadata.Versions,
		packageWrapper.UpstreamYaml.Fetch,
		packageWrapper.UpstreamYaml.TrackVersions,
	)
	if err != nil {
		return false, err
	}

	packageWrapper.LatestStored, err = getLatestStoredVersion(packageWrapper.Name)
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

func annotate(vendor, chartName, annotation, value string, remove, onlyLatest bool) error {
	var versionsToUpdate repo.ChartVersions

	allStoredVersions, err := getStoredVersions(chartName)
	if err != nil {
		return err
	}

	if onlyLatest {
		versionsToUpdate = repo.ChartVersions{allStoredVersions[0]}
	} else {
		versionsToUpdate = allStoredVersions
	}

	for _, version := range versionsToUpdate {
		modified := false

		assetsPath := filepath.Join(
			getRepoRoot(),
			repositoryAssetsDir,
			vendor,
		)

		versionPath := path.Join(
			getRepoRoot(),
			repositoryChartsDir,
			vendor,
			chartName,
		)
		helmChart, err := loader.LoadFile(version.URLs[0])
		if err != nil {
			return err
		}

		if remove {
			modified = conform.RemoveChartAnnotations(helmChart, map[string]string{annotation: value})
		} else {
			modified = conform.ApplyChartAnnotations(helmChart, map[string]string{annotation: value}, true)
		}

		if modified {
			logrus.Debugf("Modified annotations of %s (%s)\n", chartName, helmChart.Metadata.Version)

			err = os.RemoveAll(versionPath)
			if err != nil {
				return err
			}

			_, err := chartutil.Save(helmChart, assetsPath)
			if err != nil {
				return fmt.Errorf("failed to save chart %q version %q: %w", helmChart.Name(), helmChart.Metadata.Version, err)
			}
			err = conform.ExportChartDirectory(helmChart, versionPath)
			if err != nil {
				return err
			}

			err = removeVersionFromIndex(chartName, *version)
			if err != nil {
				return err
			}
		}

	}

	return err
}

// Fetches absolute repository root path
func getRepoRoot() string {
	repoRoot, err := os.Getwd()
	if err != nil {
		logrus.Fatal(err)
	}

	return repoRoot
}

func gitCleanup() error {
	r, err := git.PlainOpen(getRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	cleanOptions := git.CleanOptions{
		Dir: true,
	}

	branch, err := r.Head()
	if err != nil {
		return err
	}

	logrus.Debugf("Branch: %s\n", branch.Name())
	checkoutOptions := git.CheckoutOptions{
		Branch: branch.Name(),
		Force:  true,
	}

	err = wt.Clean(&cleanOptions)
	if err != nil {
		return err
	}

	err = wt.Checkout(&checkoutOptions)

	return err
}

// Commits changes to index file, assets, charts, and packages
func commitChanges(updatedList PackageList, iconOverride bool) error {
	var additions, updates string
	commitOptions := git.CommitOptions{}

	r, err := git.PlainOpen(getRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	logrus.Info("Committing changes")

	for _, packageWrapper := range updatedList {
		assetsPath := path.Join(
			repositoryAssetsDir,
			packageWrapper.ParsedVendor)

		chartsPath := path.Join(
			repositoryChartsDir,
			packageWrapper.ParsedVendor,
			packageWrapper.Name)

		packagesPath := path.Join(
			repositoryPackagesDir,
			packageWrapper.ParsedVendor,
			packageWrapper.Name)

		for _, path := range []string{assetsPath, chartsPath, packagesPath} {
			if _, err := wt.Add(path); err != nil {
				return fmt.Errorf("failed to add %q to working tree: %w", path, err)
			}
		}

		gitStatus, err := wt.Status()
		if err != nil {
			return err
		}

		for f, s := range gitStatus {
			if s.Worktree == git.Deleted {
				_, err = wt.Remove(f)
				if err != nil {
					return err
				}
			}
		}

	}

	if _, err := wt.Add(indexFile); err != nil {
		return fmt.Errorf("failed to add %q to working tree: %w", indexFile, err)
	}
	commitMessage := "Charts CI\n```"
	if iconOverride {
		commitMessage = "Icon Override CI\n```"
	}
	sort.Sort(updatedList)
	for _, packageWrapper := range updatedList {
		lineItem := fmt.Sprintf("  %s/%s:\n",
			packageWrapper.ParsedVendor,
			packageWrapper.Name)
		for _, version := range packageWrapper.FetchVersions {
			lineItem += fmt.Sprintf("    - %s\n", version.Version)
		}
		if packageWrapper.LatestStored.Digest == "" {
			additions += lineItem
		} else {
			updates += lineItem
		}
	}

	if additions != "" {
		commitMessage += fmt.Sprintf("\nAdded:\n%s", additions)
	}
	if updates != "" {
		commitMessage += fmt.Sprintf("\nUpdated:\n%s", updates)
	}

	commitMessage += "```"

	_, err = wt.Commit(commitMessage, &commitOptions)
	if err != nil {
		return err
	}

	gitStatus, err := wt.Status()
	if err != nil {
		return err
	}

	if !gitStatus.IsClean() {
		logrus.Fatal("Git status is not clean")
	}

	return nil
}

func collectTrackedVersions(upstreamVersions repo.ChartVersions, tracked []string) map[string]repo.ChartVersions {
	trackedVersions := make(map[string]repo.ChartVersions)

	for _, trackedVersion := range tracked {
		versionList := make(repo.ChartVersions, 0)
		for _, version := range upstreamVersions {
			semVer, err := semver.NewVersion(version.Version)
			if err != nil {
				logrus.Errorf("%s: %s", version.Version, err)
				continue
			}
			trackedSemVer, err := semver.NewVersion(trackedVersion)
			if err != nil {
				logrus.Errorf("%s: %s", version.Version, err)
				continue
			}
			logrus.Debugf("Comparing upstream version %s (%s) to tracked version %s\n", version.Name, version.Version, trackedVersion)
			if semVer.Major() == trackedSemVer.Major() && semVer.Minor() == trackedSemVer.Minor() {
				logrus.Debugf("Appending version %s tracking %s\n", version.Version, trackedVersion)
				versionList = append(versionList, version)
			} else if semVer.Major() < trackedSemVer.Major() || (semVer.Major() == trackedSemVer.Major() && semVer.Minor() < trackedSemVer.Minor()) {
				break
			}
		}
		trackedVersions[trackedVersion] = versionList
	}

	return trackedVersions
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

func checkNewerUntracked(tracked []string, upstreamVersions repo.ChartVersions) []string {
	newerUntracked := make([]string, 0)
	latestTracked := getLatestTracked(tracked)
	logrus.Debugf("Tracked Versions: %s\n", tracked)
	logrus.Debugf("Checking for versions newer than latest tracked %s\n", latestTracked)
	if len(tracked) == 0 {
		return newerUntracked
	}
	for _, upstreamVersion := range upstreamVersions {
		semVer, err := semver.NewVersion(upstreamVersion.Version)
		if err != nil {
			logrus.Error(err)
		}
		if semVer.Major() > latestTracked.Major() || (semVer.Major() == latestTracked.Major() && semVer.Minor() > latestTracked.Minor()) {
			logrus.Debugf("Found version %s newer than latest tracked %s", semVer.String(), latestTracked.String())
			newerUntracked = append(newerUntracked, semVer.String())
		} else if semVer.Major() == latestTracked.Major() && semVer.Minor() == latestTracked.Minor() {
			break
		}
	}

	return newerUntracked

}

func filterVersions(upstreamVersions repo.ChartVersions, fetch string, tracked []string) (repo.ChartVersions, error) {
	logrus.Debugf("Filtering versions for %s\n", upstreamVersions[0].Name)
	upstreamVersions = stripPreRelease(upstreamVersions)
	if len(tracked) > 0 {
		if newerUntracked := checkNewerUntracked(tracked, upstreamVersions); len(newerUntracked) > 0 {
			logrus.Warnf("Newer untracked version available: %s (%s)", upstreamVersions[0].Name, strings.Join(newerUntracked, ", "))
		} else {
			logrus.Debug("No newer untracked versions found")
		}
	}
	if len(upstreamVersions) == 0 {
		err := fmt.Errorf("No versions available in upstream or all versions are marked pre-release")
		return repo.ChartVersions{}, err
	}
	filteredVersions := make(repo.ChartVersions, 0)
	allStoredVersions, err := getStoredVersions(upstreamVersions[0].Name)
	if len(tracked) > 0 {
		allTrackedVersions := collectTrackedVersions(upstreamVersions, tracked)
		storedTrackedVersions := collectTrackedVersions(allStoredVersions, tracked)
		if err != nil {
			return filteredVersions, err
		}
		for _, trackedVersion := range tracked {
			nonStoredVersions := collectNonStoredVersions(allTrackedVersions[trackedVersion], storedTrackedVersions[trackedVersion], fetch)
			filteredVersions = append(filteredVersions, nonStoredVersions...)
		}
	} else {
		filteredVersions = collectNonStoredVersions(upstreamVersions, allStoredVersions, fetch)
	}

	return filteredVersions, nil
}

// Generates source metadata representation based on upstream repository
func generateChartSourceMetadata(upstreamYaml parse.UpstreamYaml) (*fetcher.ChartSourceMetadata, error) {
	sourceMetadata, err := fetcher.FetchUpstream(upstreamYaml)
	if err != nil {
		return nil, err
	}

	return &sourceMetadata, nil
}

func ApplyUpdates(packageWrapper PackageWrapper) error {
	logrus.Debugf("Applying updates for package %s/%s\n", packageWrapper.ParsedVendor, packageWrapper.Name)

	existingCharts, err := loadExistingCharts(packageWrapper.ParsedVendor, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to load existing charts: %w", err)
	}

	// for new charts, convert repo.ChartVersions to *chart.Chart
	newCharts := make([]*ChartWrapper, 0, len(packageWrapper.FetchVersions))
	for _, chartVersion := range packageWrapper.FetchVersions {
		var newChart *chart.Chart
		var err error
		if packageWrapper.SourceMetadata.Source == "Git" {
			newChart, err = fetcher.LoadChartFromGit(chartVersion.URLs[0], packageWrapper.SourceMetadata.SubDirectory, packageWrapper.SourceMetadata.Commit)
		} else {
			newChart, err = fetcher.LoadChartFromUrl(chartVersion.URLs[0])
		}
		if err != nil {
			return fmt.Errorf("failed to fetch chart: %w", err)
		}
		newChart.Metadata.Version = chartVersion.Version
		newCharts = append(newCharts, NewChartWrapper(newChart))
	}

	if err := integrateCharts(packageWrapper, existingCharts, newCharts); err != nil {
		return fmt.Errorf("failed to reconcile charts for package %q: %w", packageWrapper.Name, err)
	}

	allCharts := make([]*ChartWrapper, 0, len(existingCharts)+len(newCharts))
	allCharts = append(allCharts, existingCharts...)
	allCharts = append(allCharts, newCharts...)
	if err := writeCharts(packageWrapper, allCharts); err != nil {
		return fmt.Errorf("failed to write charts: %w", err)
	}

	return nil
}

// Copied from helm's chartutil.Save, which unfortunately does
// not split it out into a separate function.
func getTgzFilename(helmChart *chart.Chart) string {
	return fmt.Sprintf("%s-%s.tgz", helmChart.Name(), helmChart.Metadata.Version)
}

func writeCharts(packageWrapper PackageWrapper, chartWrappers []*ChartWrapper) error {
	chartsDir := filepath.Join(getRepoRoot(), repositoryChartsDir, packageWrapper.ParsedVendor, packageWrapper.Name)
	assetsDir := filepath.Join(getRepoRoot(), repositoryAssetsDir, packageWrapper.ParsedVendor)

	if err := os.RemoveAll(chartsDir); err != nil {
		return fmt.Errorf("failed to wipe existing charts directory: %w", err)
	}

	for _, chartWrapper := range chartWrappers {
		assetsFilename := getTgzFilename(chartWrapper.Chart)
		assetsPath := filepath.Join(assetsDir, assetsFilename)
		tgzFileExists := icons.Exists(assetsPath)
		if chartWrapper.Modified || !tgzFileExists {
			_, err := chartutil.Save(chartWrapper.Chart, assetsDir)
			if err != nil {
				return fmt.Errorf("failed to write tgz for %q version %q: %w", chartWrapper.Name(), chartWrapper.Metadata.Version, err)
			}
		}

		chartsPath := filepath.Join(chartsDir, chartWrapper.Metadata.Version)
		chartsPathExists := icons.Exists(chartsPath)
		if chartWrapper.Modified || !chartsPathExists {
			if err := conform.Gunzip(assetsPath, chartsPath); err != nil {
				return fmt.Errorf("failed to unpack %q version %q to %q: %w", chartWrapper.Name(), chartWrapper.Metadata.Version, chartsPath, err)
			}
		}
	}

	return nil
}

func loadExistingCharts(vendor string, packageName string) ([]*ChartWrapper, error) {
	assetsPath := filepath.Join(getRepoRoot(), repositoryAssetsDir, vendor)
	tgzFiles, err := os.ReadDir(assetsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []*ChartWrapper{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read dir %q: %w", assetsPath, err)
	}
	existingChartWrappers := make([]*ChartWrapper, 0, len(tgzFiles))
	for _, tgzFile := range tgzFiles {
		if tgzFile.IsDir() {
			continue
		}
		matchName := filepath.Base(tgzFile.Name())
		if matched, err := filepath.Match(fmt.Sprintf("%s-*.tgz", packageName), matchName); err != nil {
			return nil, fmt.Errorf("failed to check match for %q: %w", matchName, err)
		} else if !matched {
			continue
		}
		existingChartPath := filepath.Join(assetsPath, tgzFile.Name())
		existingChart, err := loader.LoadFile(existingChartPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load chart version %q: %w", existingChartPath, err)
		}
		existingChartWrapper := NewChartWrapper(existingChart)
		existingChartWrappers = append(existingChartWrappers, existingChartWrapper)
	}
	return existingChartWrappers, nil
}

// integrateCharts integrates new charts from upstream with any
// existing charts. It applies modifications to the new charts, and
// ensures that the state of all charts, both current and new, is
// correct. Should never modify an existing chart, except for in
// the special case of the "featured" annotation.
func integrateCharts(packageWrapper PackageWrapper, existingCharts, newCharts []*ChartWrapper) error {
	overlayFiles, err := packageWrapper.GetOverlayFiles()
	if err != nil {
		return fmt.Errorf("failed to get overlay files: %w", err)
	}

	for _, newChart := range newCharts {
		if err := applyOverlayFiles(overlayFiles, newChart.Chart); err != nil {
			return fmt.Errorf("failed to apply overlay files to chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
		conform.OverlayChartMetadata(newChart.Chart, packageWrapper.UpstreamYaml.ChartYaml)
		if err := addAnnotations(packageWrapper, newChart.Chart); err != nil {
			return fmt.Errorf("failed to add annotations to chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
		if err := ensureIcon(packageWrapper, newChart.Chart); err != nil {
			return fmt.Errorf("failed to ensure icon for chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
		newChart.Modified = true
	}

	if err := ensureFeaturedAnnotation(existingCharts, newCharts); err != nil {
		return fmt.Errorf("failed to ensure featured annotation: %w", err)
	}

	return nil
}

// applyOverlayFiles applies the files referenced in overlayFiles to the files
// in helmChart.Files. If a file already exists, it is overwritten.
func applyOverlayFiles(overlayFiles map[string][]byte, helmChart *chart.Chart) error {
	for relativePath, contents := range overlayFiles {
		newFile := &chart.File{
			Name: relativePath,
			Data: contents,
		}
		for _, file := range helmChart.Files {
			if file.Name == relativePath {
				file.Data = contents
				goto skip
			}
		}
		helmChart.Files = append(helmChart.Files, newFile)
	skip:
	}
	return nil
}

// Ensures that an icon for the chart has been downloaded to the local icons
// directory, and that the icon URL field for helmChart refers to this local
// icon file. We do this so that airgap installations of Rancher have access
// to icons without needing to download them from a remote source.
func ensureIcon(packageWrapper PackageWrapper, helmChart *chart.Chart) error {
	if localIconUrl, err := icons.GetDownloadedIconPath(packageWrapper.Name); err == nil {
		helmChart.Metadata.Icon = localIconUrl
		return nil
	}

	localIconPath, err := icons.DownloadIcon(helmChart.Metadata.Icon, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to download icon: %w", err)
	}

	helmChart.Metadata.Icon = "file://" + localIconPath
	return nil
}

// Sets annotations on helmChart according to values from packageWrapper,
// and especially from packageWrapper.UpstreamYaml.
func addAnnotations(packageWrapper PackageWrapper, helmChart *chart.Chart) error {
	annotations := make(map[string]string)

	if autoInstall := packageWrapper.UpstreamYaml.AutoInstall; autoInstall != "" {
		annotations[annotationAutoInstall] = autoInstall
	}

	if packageWrapper.UpstreamYaml.Experimental {
		annotations[annotationExperimental] = "true"
	}

	if packageWrapper.UpstreamYaml.Hidden {
		annotations[annotationHidden] = "true"
	}

	// TODO: this is sketchy. We end up changing the repository URL of each
	// dependency without downloading dependencies. This can't be right.
	// And if it is, this needs a comment explaining what is going on.
	// Need to investigate further.
	if !packageWrapper.UpstreamYaml.RemoteDependencies {
		for _, d := range helmChart.Metadata.Dependencies {
			d.Repository = fmt.Sprintf("file://./charts/%s", d.Name)
		}
	}

	annotations[annotationCertified] = "partner"

	annotations[annotationDisplayName] = packageWrapper.DisplayName

	if packageWrapper.UpstreamYaml.ReleaseName != "" {
		annotations[annotationReleaseName] = packageWrapper.UpstreamYaml.ReleaseName
	} else {
		annotations[annotationReleaseName] = packageWrapper.Name
	}

	if packageWrapper.UpstreamYaml.Namespace != "" {
		annotations[annotationNamespace] = packageWrapper.UpstreamYaml.Namespace
	}

	if packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
		annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
	} else if helmChart.Metadata.KubeVersion != "" {
		annotations[annotationKubeVersion] = helmChart.Metadata.KubeVersion
	}

	if packageVersion := packageWrapper.UpstreamYaml.PackageVersion; packageVersion != 0 {
		generatedVersion, err := conform.GeneratePackageVersion(helmChart.Metadata.Version, &packageVersion)
		helmChart.Metadata.Version = generatedVersion
		if err != nil {
			return fmt.Errorf("failed to generate version: %w", err)
		}
	}

	conform.ApplyChartAnnotations(helmChart, annotations, false)

	return nil
}

// Ensures that "featured" annotation is set properly for the set of all passed
// charts. Is separate from setting other annotations because only the latest
// chart version for a given package must have the "featured" annotation, so
// this function must consider and possibly modify all of the package's chart
// versions.
func ensureFeaturedAnnotation(existingCharts, newCharts []*ChartWrapper) error {
	// get current value of featured annotation
	featuredAnnotationValue := ""
	for _, existingChart := range existingCharts {
		val, ok := existingChart.Metadata.Annotations[annotationFeatured]
		if !ok {
			continue
		}
		if featuredAnnotationValue != "" && featuredAnnotationValue != val {
			return fmt.Errorf("found two different values for featured annotation %q and %q", featuredAnnotationValue, val)
		}
		featuredAnnotationValue = val
	}
	if featuredAnnotationValue == "" {
		// the chart is not featured
		return nil
	}

	// set featured annotation on last of new charts
	// TODO: This replicates a bug in the existing code. Whichever ChartVersion
	// comes last in the ChartVersions that conformPackage is working on has
	// the featured annotation applies. This could easily give the wrong result, which
	// presumably is for only the latest chart version to have the "featured"
	// annotation.
	// But in practice this is not a problem: as of the time of writing, only
	// one chart (kasten/k10) uses a value for UpstreamYaml.Fetch other than the
	// default value of "latest", and that chart is not featured.
	lastNewChart := newCharts[len(newCharts)-1]
	if conform.AnnotateChart(lastNewChart.Chart, annotationFeatured, featuredAnnotationValue, true) {
		lastNewChart.Modified = true
	}

	// Ensure featured annotation is not present on existing charts. We don't
	// need to worry about other new charts because they will not have the
	// featured annotation.
	for _, existingChart := range existingCharts {
		if conform.DeannotateChart(existingChart.Chart, annotationFeatured, "") {
			existingChart.Modified = true
		}
	}

	return nil
}

func getLatestTracked(tracked []string) *semver.Version {
	var latestTracked *semver.Version
	for _, version := range tracked {
		semVer, err := semver.NewVersion(version)
		if err != nil {
			logrus.Error(err)
		}
		if latestTracked == nil || semVer.GreaterThan(latestTracked) {
			latestTracked = semVer
		}
	}

	return latestTracked
}

func getStoredVersions(chartName string) (repo.ChartVersions, error) {
	helmIndexYaml, err := readIndex()
	storedVersions := repo.ChartVersions{}
	if err != nil {
		return storedVersions, err
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		storedVersions = append(storedVersions, val...)
	}

	return storedVersions, nil
}

// Fetches latest stored version of chart from current index, if any
func getLatestStoredVersion(chartName string) (repo.ChartVersion, error) {
	helmIndexYaml, err := readIndex()
	latestVersion := repo.ChartVersion{}
	if err != nil {
		return latestVersion, err
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		latestVersion = *val[0]
	}

	return latestVersion, nil
}

// getByAnnotation gets all repo.ChartVersions from index.yaml that have
// the specified annotation with the specified value. If value is "",
// all repo.ChartVersions that have the specified annotation will be
// returned, regardless of that annotation's value.
func getByAnnotation(annotation, value string) map[string]repo.ChartVersions {
	indexYaml, err := readIndex()
	if err != nil {
		logrus.Fatalf("failed to read index.yaml: %s", err)
	}
	matchedVersions := make(map[string]repo.ChartVersions)

	for chartName, entries := range indexYaml.Entries {
		for _, version := range entries {
			appendVersion := false
			if _, ok := version.Annotations[annotation]; ok {
				if value != "" {
					if version.Annotations[annotation] == value {
						appendVersion = true
					}
				} else {
					appendVersion = true
				}
			}
			if appendVersion {
				if _, ok := matchedVersions[chartName]; !ok {
					matchedVersions[chartName] = repo.ChartVersions{version}
				} else {
					matchedVersions[chartName] = append(matchedVersions[chartName], version)
				}
			}
		}
	}

	return matchedVersions
}

func removeVersionFromIndex(chartName string, version repo.ChartVersion) error {
	entryIndex := -1
	indexYaml, err := readIndex()
	if err != nil {
		return err
	}
	if _, ok := indexYaml.Entries[chartName]; !ok {
		return fmt.Errorf("%s not present in index entries", chartName)
	}

	indexEntries := indexYaml.Entries[chartName]

	for i, entryVersion := range indexEntries {
		if entryVersion.Version == version.Version {
			entryIndex = i
			break
		}
	}

	if entryIndex >= 0 {
		entries := make(repo.ChartVersions, 0)
		entries = append(entries, indexEntries[:entryIndex]...)
		entries = append(entries, indexEntries[entryIndex+1:]...)
		indexYaml.Entries[chartName] = entries
	} else {
		return fmt.Errorf("version %s not found for chart %s in index", version.Version, chartName)
	}

	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	err = indexYaml.WriteFile(indexFilePath, 0644)

	return err
}

// Reads in current index yaml
func readIndex() (*repo.IndexFile, error) {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	return helmIndexYaml, err
}

// writeIndex is the only way that index.yaml should ever be written.
// It looks at the set of charts in the assets directory and generates
// a new index.yaml file from their metadata. Some information from
// the old index.yaml file is used to avoid making unnecessary changes,
// but for the most part this function enforces the idea that the
// index.yaml file should treat the charts' Chart.yaml files as the
// authoritative source of chart metadata.
func writeIndex() error {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	assetsDirectoryPath := filepath.Join(getRepoRoot(), repositoryAssetsDir)
	newHelmIndexYaml, err := repo.IndexDirectory(assetsDirectoryPath, repositoryAssetsDir)
	if err != nil {
		return fmt.Errorf("failed to index assets directory: %w", err)
	}

	oldHelmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	if errors.Is(err, os.ErrNotExist) {
		if err := newHelmIndexYaml.WriteFile(indexFilePath, 0o644); err != nil {
			return fmt.Errorf("failed to write index.yaml: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to load index.yaml: %w", err)
	}

	for chartName, newChartVersions := range newHelmIndexYaml.Entries {
		for _, newChartVersion := range newChartVersions {
			// Use the values of created field from old index.yaml to avoid making
			// unnecessary changes, since it is set to time.Now() in repo.LoadIndexFile.
			oldChartVersion, err := oldHelmIndexYaml.Get(chartName, newChartVersion.Version)
			if err == nil {
				newChartVersion.Created = oldChartVersion.Created
			}

			// Older charts cannot be changed, and may have remote (i.e. not
			// beginning with file://) icon URLs. So instead of changing the
			// icon URL in the Chart.yaml and allowing it to propagate automatically
			// to the index.yaml for these chart versions, we change it only in
			// the index.yaml. This works because Rancher uses the icon URL
			// value from index.yaml, not the chart itself, when loading a chart's
			// icon.
			iconURL, err := icons.GetDownloadedIconPath(newChartVersion.Name)
			if err != nil {
				// TODO: return an error here instead of simply logging it.
				// Logged errors can be ignored; errors that prevent the user
				// from completing their task get fixed. But the errors in
				// rancher/partner-charts must be addressed before we can
				// do this.
				logrus.Errorf("failed to get downloaded icon path for chart %q version %q: %s", newChartVersion.Name, newChartVersion.Version, err)
			} else {
				newChartVersion.Icon = iconURL
			}
		}
	}

	newHelmIndexYaml.SortEntries()

	if err := newHelmIndexYaml.WriteFile(indexFilePath, 0o644); err != nil {
		return fmt.Errorf("failed to write index.yaml: %w", err)
	}

	return nil
}

// listPackageWrappers reads packages and their upstream.yaml from the packages
// directory and returns them in a slice. If currentPackage is specified,
// it must be in <vendor>/<name> format (i.e. the "full" package name).
// If currentPackage is specified, the function returns a slice with only
// one element, which is the specified package.
func listPackageWrappers(currentPackage string) (PackageList, error) {
	var globPattern string
	if currentPackage == "" {
		globPattern = repositoryPackagesDir + "/*/*"
	} else {
		globPattern = filepath.Join(repositoryPackagesDir, currentPackage)
	}
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob for packages")
	}

	packageList := make(PackageList, 0, len(matches))
	for _, match := range matches {
		parts := strings.Split(match, "/")
		if len(parts) != 3 {
			return nil, fmt.Errorf("failed to split %q into 3 parts", match)
		}
		packageWrapper := PackageWrapper{
			Path:         match,
			ParsedVendor: parts[1],
			Name:         parts[2],
		}

		upstreamYaml, err := parse.ParseUpstreamYaml(packageWrapper.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to parse upstream.yaml: %w", err)
		}
		packageWrapper.UpstreamYaml = &upstreamYaml

		if packageWrapper.UpstreamYaml.Vendor != "" {
			packageWrapper.Vendor = packageWrapper.UpstreamYaml.Vendor
		} else {
			packageWrapper.Vendor = packageWrapper.ParsedVendor
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

// Populates list of package wrappers, handles manual and automatic variation
// If print, function will print information during processing
func populatePackages(currentPackage string, onlyUpdates bool, onlyLatest bool, print bool) (PackageList, error) {
	packageList := make(PackageList, 0)
	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}
	for _, packageWrapper := range packageWrappers {
		logrus.Debugf("Populating package from %s\n", packageWrapper.Path)
		updated, err := packageWrapper.populate(onlyLatest)
		if err != nil {
			logrus.Error(err)
			continue
		}
		if print {
			logrus.Infof("Parsed %s/%s\n", packageWrapper.ParsedVendor, packageWrapper.Name)
			if len(packageWrapper.FetchVersions) == 0 {
				logrus.Infof("%s (%s) is up-to-date\n",
					packageWrapper.Vendor, packageWrapper.Name)
			}
			for _, version := range packageWrapper.FetchVersions {
				logrus.Infof("\n  Source: %s\n  Vendor: %s\n  Chart: %s\n  Version: %s\n  URL: %s  \n",
					packageWrapper.SourceMetadata.Source, packageWrapper.Vendor, packageWrapper.Name,
					version.Version, version.URLs[0])
			}
		}

		if onlyUpdates && !updated {
			continue
		}

		packageList = append(packageList, packageWrapper)
	}

	return packageList, nil
}

// downloadIcons should only be used in a local machine by manual execution.
// It will download all icons that contain URLs from the index.yaml file, if it is already downloaded it will keep it.
// All downloaded icons will be saved in the assets/icons directory.
func downloadIcons(c *cli.Context) {
	currentPackage := os.Getenv(packageEnvVariable)
	icons.CheckFilesStructure() // stop execution if file structure is not correct

	packageList, err := populatePackages(currentPackage, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	// Convert packageList to PackageIconMap
	var entriesPathsAndIconsMap icons.PackageIconMap = make(icons.PackageIconMap)
	for _, pkg := range packageList {
		entriesPathsAndIconsMap[pkg.Name] = icons.PackageIconOverride{
			Name: pkg.Name,
			Path: pkg.Path,
			Icon: pkg.LatestStored.Metadata.Icon,
		}
	}

	// Download all icons or retrieve the ones already downloaded
	downloadedIcons := icons.DownloadFiles(entriesPathsAndIconsMap)

	logrus.Infof("Finished downloading and saving icon files")
	logrus.Infof("Downloaded %d icons", len(downloadedIcons))
}

// generateChanges will generate the changes for the packages based on the flags provided
// if auto or stage is true, it will write the index.yaml file if the chart has new updates
// the charts to be modified depends on the populatePackages function and their update status
// the changes will be applied on fetchUpstreams function
func generateChanges(auto bool) {
	currentPackage := os.Getenv(packageEnvVariable)
	var packageList PackageList
	packageList, err := populatePackages(currentPackage, true, false, true)
	if err != nil {
		logrus.Fatal(err)
	}

	if len(packageList) == 0 {
		return
	}

	skippedList := make([]string, 0)
	for _, packageWrapper := range packageList {
		if err := ApplyUpdates(packageWrapper); err != nil {
			logrus.Errorf("failed to apply updates for chart %q: %s", packageWrapper.Name, err)
			skippedList = append(skippedList, packageWrapper.Name)
		}
	}
	if len(skippedList) > 0 {
		logrus.Errorf("Skipped due to error: %v", skippedList)
	}
	if len(skippedList) >= len(packageList) {
		logrus.Fatalf("All packages skipped. Exiting...")
	}

	if err := writeIndex(); err != nil {
		logrus.Error(err)
	}

	if auto {
		err = commitChanges(packageList, false)
		if err != nil {
			logrus.Fatal(err)
		}
	}
}

// CLI function call - Prints list of available packages to STDout
func listPackages(c *cli.Context) error {
	packageList, err := listPackageWrappers(os.Getenv(packageEnvVariable))
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	vendorSorted := make([]string, 0)
	for _, packageWrapper := range packageList {
		packagesPath := filepath.Join(getRepoRoot(), repositoryPackagesDir)
		packageParentPath := filepath.Dir(packageWrapper.Path)
		packageRelativePath := filepath.Base(packageWrapper.Path)
		if packagesPath != packageParentPath {
			packageRelativePath = filepath.Join(filepath.Base(packageParentPath), packageRelativePath)
		}
		vendorSorted = append(vendorSorted, packageRelativePath)
	}

	sort.Strings(vendorSorted)
	for _, pkg := range vendorSorted {
		fmt.Println(pkg)
	}

	return nil
}

// CLI function call - Appends annotaion to feature chart in Rancher UI
func addFeaturedChart(c *cli.Context) error {
	if len(c.Args()) != 2 {
		logrus.Fatalf("Please provide the chart name and featured number (1 - %d) as arguments\n", featuredMax)
	}
	featuredChart := c.Args().Get(0)
	featuredNumber, err := strconv.Atoi(c.Args().Get(1))
	if err != nil {
		logrus.Fatal(err)
	}
	if featuredNumber < 1 || featuredNumber > featuredMax {
		logrus.Fatalf("Featured number must be between %d and %d\n", 1, featuredMax)
	}

	packageList, err := listPackageWrappers(featuredChart)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	if len(packageList) == 0 {
		return fmt.Errorf("package %q not available\n", featuredChart)
	}

	packageList, err = populatePackages(featuredChart, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	featuredVersions := getByAnnotation(annotationFeatured, c.Args().Get(1))

	if len(featuredVersions) > 0 {
		for chartName := range featuredVersions {
			logrus.Errorf("%s already featured at index %d\n", chartName, featuredNumber)
		}
	} else {
		vendor := packageList[0].ParsedVendor
		chartName := packageList[0].LatestStored.Name
		err = annotate(vendor, chartName, annotationFeatured, c.Args().Get(1), false, true)
		if err != nil {
			logrus.Fatal(err)
		}
		if err = writeIndex(); err != nil {
			logrus.Fatalf("failed to write index: %s", err)
		}
	}

	return nil
}

// CLI function call - Appends annotaion to feature chart in Rancher UI
func removeFeaturedChart(c *cli.Context) {
	if len(c.Args()) != 1 {
		logrus.Fatal("Please provide the chart name as argument")
	}
	featuredChart := c.Args().Get(0)
	packageMap, err := parse.ListPackages(repositoryPackagesDir, "")
	if err != nil {
		logrus.Fatal(err)
	}
	if _, ok := packageMap[featuredChart]; !ok {
		logrus.Fatalf("Package '%s' not available\n", featuredChart)
	}

	packageList, err := populatePackages(featuredChart, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	vendor := packageList[0].ParsedVendor
	chartName := packageList[0].LatestStored.Name
	err = annotate(vendor, chartName, annotationFeatured, "", true, false)
	if err != nil {
		logrus.Fatal(err)
	}
	if err = writeIndex(); err != nil {
		logrus.Fatalf("failed to write index: %s", err)
	}
}

func listFeaturedCharts(c *cli.Context) {
	indexConflict := false
	featuredSorted := make([]string, featuredMax)
	featuredVersions := getByAnnotation(annotationFeatured, "")

	for chartName, chartVersion := range featuredVersions {
		featuredIndex, err := strconv.Atoi(chartVersion[0].Annotations[annotationFeatured])
		if err != nil {
			logrus.Fatal(err)
		}
		featuredIndex--
		if featuredSorted[featuredIndex] != "" {
			indexConflict = true
			featuredSorted[featuredIndex] += fmt.Sprintf(", %s", chartName)
		} else {
			featuredSorted[featuredIndex] = chartName
		}
	}
	if indexConflict {
		logrus.Errorf("Multiple charts given same featured index")
	}

	for i, chartName := range featuredSorted {
		if featuredSorted[i] != "" {
			fmt.Printf("%d: %s\n", i+1, chartName)
		}
	}

}

// CLI function call - Appends annotation to hide chart in Rancher UI
func hideChart(c *cli.Context) {
	if len(c.Args()) < 1 {
		logrus.Fatal("Provide package name(s) as argument")
	}
	for _, currentPackage := range c.Args() {
		packageList, err := populatePackages(currentPackage, false, false, false)
		if err != nil {
			logrus.Error(err)
		}

		if len(packageList) == 1 {
			vendor := packageList[0].ParsedVendor
			chartName := packageList[0].LatestStored.Name
			err = annotate(vendor, chartName, annotationHidden, "true", false, false)
			if err != nil {
				logrus.Error(err)
			}
			if err = writeIndex(); err != nil {
				logrus.Fatalf("failed to write index: %s", err)
			}
		}
	}
}

// CLI function call - Generates all changes for available packages,
// Checking against upstream version, prepare, patch, clean, and index update
// Does not commit
func stageChanges(c *cli.Context) {
	generateChanges(false)
}

func unstageChanges(c *cli.Context) {
	err := gitCleanup()
	if err != nil {
		logrus.Error(err)
	}
}

// CLI function call - Generates automated commit
func autoUpdate(c *cli.Context) {
	generateChanges(true)
}

// CLI function call - Validates repo against released
func validateRepo(c *cli.Context) {
	validatePaths := map[string]validate.DirectoryComparison{
		"assets": {},
	}

	excludeFiles := make(map[string]struct{})
	var exclude = struct{}{}
	excludeFiles["README.md"] = exclude

	directoryComparison := validate.DirectoryComparison{}

	configYamlPath := path.Join(getRepoRoot(), configOptionsFile)
	configYaml, err := validate.ReadConfig(configYamlPath)
	if err != nil {
		logrus.Fatalf("failed to read %s: %s\n", configOptionsFile, err)
	}

	if len(configYaml.Validate) == 0 || configYaml.Validate[0].Branch == "" || configYaml.Validate[0].Url == "" {
		logrus.Fatal("Invalid validation configuration")
	}

	cloneDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		logrus.Fatal(err)
	}

	err = validate.CloneRepo(configYaml.Validate[0].Url, configYaml.Validate[0].Branch, cloneDir)
	if err != nil {
		logrus.Fatal(err)
	}

	for dirPath := range validatePaths {
		upstreamPath := path.Join(cloneDir, dirPath)
		updatePath := path.Join(getRepoRoot(), dirPath)
		if _, err := os.Stat(updatePath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in source. Skipping...", dirPath)
			continue
		}
		if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in upstream. Skipping...", dirPath)
			continue
		}
		newComparison, err := validate.CompareDirectories(upstreamPath, updatePath, excludeFiles)
		if err != nil {
			logrus.Error(err)
		}
		directoryComparison.Merge(newComparison)
		validatePaths[dirPath] = newComparison
	}

	err = os.RemoveAll(cloneDir)
	if err != nil {
		logrus.Error(err)
	}

	if len(directoryComparison.Added) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Added) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Added[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Infof("Files Added:%s", outString)
	}

	if len(directoryComparison.Removed) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Removed) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Removed[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Warnf("Files Removed:%s", outString)
	}

	if len(directoryComparison.Modified) > 0 {
		outString := ""
		for dirPath := range validatePaths {
			if len(validatePaths[dirPath].Modified) > 0 {
				outString += fmt.Sprintf("\n - %s", dirPath)
				stringJoiner := fmt.Sprintf("\n - %s", dirPath)
				fileList := strings.Join(validatePaths[dirPath].Modified[:], stringJoiner)
				outString += fileList
			}
		}
		logrus.Fatalf("Files Modified:%s", outString)
	}

	logrus.Infof("Successfully validated\n  Upstream: %s\n  Branch: %s\n",
		configYaml.Validate[0].Url, configYaml.Validate[0].Branch)

}

func cullCharts(c *cli.Context) error {
	// get the name of the chart to work on
	chartName := c.Args().Get(0)

	// parse days argument
	rawDays := c.Args().Get(1)
	daysInt64, err := strconv.ParseInt(rawDays, 10, strconv.IntSize)
	if err != nil {
		return fmt.Errorf("failed to convert %q to integer: %w", rawDays, err)
	}
	days := int(daysInt64)

	// parse index.yaml
	index, err := repo.LoadIndexFile(indexFile)
	if err != nil {
		return fmt.Errorf("failed to read index file: %w", err)
	}

	// try to find subjectPackage in index.yaml
	packageVersions, ok := index.Entries[chartName]
	if !ok {
		return fmt.Errorf("chart %q not present in %s", chartName, indexFile)
	}

	// get charts that are newer and older than cutoff
	now := time.Now()
	cutoff := now.AddDate(0, 0, -days)
	olderPackageVersions := make(repo.ChartVersions, 0, len(packageVersions))
	newerPackageVersions := make(repo.ChartVersions, 0, len(packageVersions))
	for _, packageVersion := range packageVersions {
		if packageVersion.Created.After(cutoff) {
			newerPackageVersions = append(newerPackageVersions, packageVersion)
		} else {
			olderPackageVersions = append(olderPackageVersions, packageVersion)
		}
	}

	// remove old charts from assets directory
	for _, olderPackageVersion := range olderPackageVersions {
		for _, url := range olderPackageVersion.URLs {
			if err := os.Remove(url); err != nil {
				return fmt.Errorf("failed to remove %q: %w", url, err)
			}
		}
	}

	// modify index.yaml
	index.Entries[chartName] = newerPackageVersions
	if err := index.WriteFile(indexFile, 0o644); err != nil {
		return fmt.Errorf("failed to write index file: %w", err)
	}

	return nil
}

func main() {
	if len(os.Getenv("DEBUG")) > 0 {
		logrus.SetLevel(logrus.DebugLevel)
	}

	app := cli.NewApp()
	app.Name = "partner-charts-ci"
	app.Version = fmt.Sprintf("%s (%s)", version, commit)
	app.Usage = "Assists in submission and maintenance of partner Helm charts"

	app.Commands = []cli.Command{
		{
			Name:   "list",
			Usage:  "Print a list of all tracked upstreams in current repository",
			Action: listPackages,
		},
		{
			Name:   "auto",
			Usage:  "Generate and commit changes",
			Action: autoUpdate,
		},
		{
			Name:   "stage",
			Usage:  "Stage all changes. Does not commit",
			Action: stageChanges,
		},
		{
			Name:   "unstage",
			Usage:  "Un-Stage all non-committed changes. Deletes all untracked files.",
			Action: unstageChanges,
		},
		{
			Name:   "hide",
			Usage:  "Apply 'catalog.cattle.io/hidden' annotation to all stored versions of chart",
			Action: hideChart,
		},
		{
			Name:  "feature",
			Usage: "Manipulate charts featured in Rancher UI",
			Subcommands: []cli.Command{
				{
					Name:   "list",
					Usage:  "List currently featured charts",
					Action: listFeaturedCharts,
				},
				{
					Name:   "add",
					Usage:  "Add featured annotation to chart",
					Action: addFeaturedChart,
				},
				{
					Name:   "remove",
					Usage:  "Remove featured annotation from chart",
					Action: removeFeaturedChart,
				},
			},
		},
		{
			Name:   "validate",
			Usage:  "Check repo against released charts",
			Action: validateRepo,
		},
		{
			Name:   "download-icons",
			Usage:  "Download icons from charts in index.yaml",
			Action: downloadIcons,
		},
		{
			Name:      "cull",
			Usage:     "Remove versions of chart older than a number of days",
			Action:    cullCharts,
			ArgsUsage: "<chart> <days>",
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

}
