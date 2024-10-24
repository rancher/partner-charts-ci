package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/go-git/go-git/v5"
	"github.com/rancher/partner-charts-ci/pkg/conform"
	"github.com/rancher/partner-charts-ci/pkg/fetcher"
	"github.com/rancher/partner-charts-ci/pkg/icons"
	"github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/rancher/partner-charts-ci/pkg/upstreamyaml"
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
	upstreamYamlFile      = "upstream.yaml"
)

var (
	version = "v0.0.0"
	commit  = "HEAD"
	force   = false
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
func (packageWrapper *PackageWrapper) Populate() (bool, error) {
	sourceMetadata, err := fetcher.FetchUpstream(*packageWrapper.UpstreamYaml)
	if err != nil {
		return false, fmt.Errorf("failed to fetch data from upstream: %w", err)
	}
	if sourceMetadata.Versions[0].Name != packageWrapper.Name {
		logrus.Warnf("upstream name %q does not match package name %q", sourceMetadata.Versions[0].Name, packageWrapper.Name)
	}
	packageWrapper.SourceMetadata = &sourceMetadata

	packageWrapper.FetchVersions, err = filterVersions(
		packageWrapper.SourceMetadata.Versions,
		packageWrapper.UpstreamYaml.Fetch,
		packageWrapper.UpstreamYaml.TrackVersions,
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

func annotate(vendor, chartName, annotation, value string, remove, onlyLatest bool) error {
	existingCharts, err := loadExistingCharts(paths.GetRepoRoot(), vendor, chartName)
	if err != nil {
		return fmt.Errorf("failed to load existing charts: %w", err)
	}

	chartsToUpdate := make([]*ChartWrapper, 0, len(existingCharts))
	if onlyLatest {
		chartsToUpdate = append(chartsToUpdate, existingCharts[0])
	} else {
		chartsToUpdate = existingCharts
	}

	for _, chartToUpdate := range chartsToUpdate {
		if remove {
			chartToUpdate.Modified = conform.DeannotateChart(chartToUpdate.Chart, annotation, value)
		} else {
			chartToUpdate.Modified = conform.AnnotateChart(chartToUpdate.Chart, annotation, value, true)
		}
	}

	if err := writeCharts(paths.GetRepoRoot(), vendor, chartName, existingCharts); err != nil {
		return fmt.Errorf("failed to write charts: %w", err)
	}

	return nil
}

func gitCleanup() error {
	r, err := git.PlainOpen(paths.GetRepoRoot())
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
func commitChanges(updatedList PackageList) error {
	commitOptions := git.CommitOptions{}

	r, err := git.PlainOpen(paths.GetRepoRoot())
	if err != nil {
		return err
	}

	wt, err := r.Worktree()
	if err != nil {
		return err
	}

	logrus.Info("Committing changes")

	iconsPath := filepath.Join(repositoryAssetsDir, "icons")
	if _, err := wt.Add(iconsPath); err != nil {
		return fmt.Errorf("failed to add %q to working tree: %w", iconsPath, err)
	}

	for _, packageWrapper := range updatedList {
		assetsPath := filepath.Join(repositoryAssetsDir, packageWrapper.Vendor)
		chartsPath := filepath.Join(repositoryChartsDir, packageWrapper.Vendor, packageWrapper.Name)
		packagesPath := filepath.Join(repositoryPackagesDir, packageWrapper.Vendor, packageWrapper.Name)

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
	commitMessage := "Added chart versions:\n"
	sort.Sort(updatedList)
	for _, packageWrapper := range updatedList {
		commitMessage += fmt.Sprintf("  %s:\n", packageWrapper.FullName())
		for _, version := range packageWrapper.FetchVersions {
			commitMessage += fmt.Sprintf("    - %s\n", version.Version)
		}
	}

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

func ApplyUpdates(packageWrapper PackageWrapper) error {
	logrus.Debugf("Applying updates for package %s/%s\n", packageWrapper.Vendor, packageWrapper.Name)

	existingCharts, err := loadExistingCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name)
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
	if err := writeCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name, allCharts); err != nil {
		return fmt.Errorf("failed to write charts: %w", err)
	}

	return nil
}

// Copied from helm's chartutil.Save, which unfortunately does
// not split it out into a separate function.
func getTgzFilename(helmChart *chart.Chart) string {
	return fmt.Sprintf("%s-%s.tgz", helmChart.Name(), helmChart.Metadata.Version)
}

// writeCharts ensures that the relevant assets/ and charts/
// directories for package <vendor>/<chartName> reflect the set of
// packages passed in chartWrappers. In other words, charts that are
// not in chartWrappers are deleted, and charts from chartWrappers
// that are modified or do not exist on disk are written.
func writeCharts(repoRoot, vendor, chartName string, chartWrappers []*ChartWrapper) error {
	chartsDir := filepath.Join(repoRoot, repositoryChartsDir, vendor, chartName)
	assetsDir := filepath.Join(repoRoot, repositoryAssetsDir, vendor)

	if err := os.RemoveAll(chartsDir); err != nil {
		return fmt.Errorf("failed to wipe existing charts directory: %w", err)
	}

	// delete any charts on disk that are not in chartWrappers
	existingCharts, err := loadExistingCharts(repoRoot, vendor, chartName)
	if err != nil {
		return fmt.Errorf("failed to load existing charts: %w", err)
	}
	versionToChartWrapper := map[string]*ChartWrapper{}
	for _, chartWrapper := range chartWrappers {
		versionToChartWrapper[chartWrapper.Metadata.Version] = chartWrapper
	}
	for _, existingChart := range existingCharts {
		if _, ok := versionToChartWrapper[existingChart.Metadata.Version]; !ok {
			assetFilename := getTgzFilename(existingChart.Chart)
			assetPath := filepath.Join(assetsDir, assetFilename)
			if err := os.RemoveAll(assetPath); err != nil {
				return fmt.Errorf("failed to remove %q: %w", assetFilename, err)
			}
		}
	}

	// create or update existing charts
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

// loadExistingCharts loads the existing charts for package
// <vendor>/<packageName> from the assets directory. It returns
// them in a slice that is sorted by chart version, newest first.
func loadExistingCharts(repoRoot string, vendor string, packageName string) ([]*ChartWrapper, error) {
	existingChartPaths, err := getExistingChartTgzFiles(repoRoot, vendor, packageName)
	if err != nil {
		return nil, fmt.Errorf("failed to get paths to existing chart tgz files: %w", err)
	}
	existingChartWrappers := make([]*ChartWrapper, 0, len(existingChartPaths))
	for _, existingChartPath := range existingChartPaths {
		existingChart, err := loader.LoadFile(existingChartPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load chart version %q: %w", existingChartPath, err)
		}
		existingChartWrapper := NewChartWrapper(existingChart)
		existingChartWrappers = append(existingChartWrappers, existingChartWrapper)
	}
	slices.SortFunc(existingChartWrappers, func(a, b *ChartWrapper) int {
		parsedA := semver.MustParse(a.Chart.Metadata.Version)
		parsedB := semver.MustParse(b.Chart.Metadata.Version)
		return parsedB.Compare(parsedA)
	})
	return existingChartWrappers, nil
}

// getExistingChartTgzFiles lists the .tgz files for package <vendor>/
// <packageName> from that package vendor's assets directory.
func getExistingChartTgzFiles(repoRoot string, vendor string, packageName string) ([]string, error) {
	assetsPath := filepath.Join(repoRoot, repositoryAssetsDir, vendor)
	tgzFiles, err := os.ReadDir(assetsPath)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read dir %q: %w", assetsPath, err)
	}
	filePaths := make([]string, 0, len(tgzFiles))
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
		filePaths = append(filePaths, existingChartPath)
	}
	return filePaths, nil
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
		conform.OverlayChartMetadata(newChart.Chart, packageWrapper.UpstreamYaml.ChartMetadata)
		if err := addAnnotations(packageWrapper, newChart.Chart); err != nil {
			return fmt.Errorf("failed to add annotations to chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
		if err := ensureIcon(packageWrapper, newChart); err != nil {
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
func ensureIcon(packageWrapper PackageWrapper, chartWrapper *ChartWrapper) error {
	localIconPath, err := icons.EnsureIconDownloaded(chartWrapper.Metadata.Icon, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to ensure icon downloaded: %w", err)
	}

	localIconUrl := "file://" + localIconPath
	if chartWrapper.Metadata.Icon != localIconUrl {
		chartWrapper.Metadata.Icon = localIconUrl
		chartWrapper.Modified = true
	}

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

	annotations[annotationCertified] = "partner"

	annotations[annotationDisplayName] = packageWrapper.DisplayName

	annotations[annotationReleaseName] = packageWrapper.UpstreamYaml.ReleaseName

	if packageWrapper.UpstreamYaml.Namespace != "" {
		annotations[annotationNamespace] = packageWrapper.UpstreamYaml.Namespace
	}

	if packageWrapper.UpstreamYaml.ChartMetadata.KubeVersion != "" {
		annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartMetadata.KubeVersion
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
	storedVersions := repo.ChartVersions{}
	indexFilePath := filepath.Join(paths.GetRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	if err != nil {
		return storedVersions, fmt.Errorf("failed to load index file: %w", err)
	}
	if val, ok := helmIndexYaml.Entries[chartName]; ok {
		storedVersions = append(storedVersions, val...)
	}

	return storedVersions, nil
}

// getByAnnotation gets all repo.ChartVersions from index.yaml that have
// the specified annotation with the specified value. If value is "",
// all repo.ChartVersions that have the specified annotation will be
// returned, regardless of that annotation's value.
func getByAnnotation(annotation, value string) map[string]repo.ChartVersions {
	indexFilePath := filepath.Join(paths.GetRepoRoot(), indexFile)
	indexYaml, err := repo.LoadIndexFile(indexFilePath)
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

// writeIndex is the only way that index.yaml should ever be written.
// It looks at the set of charts in the assets directory and generates
// a new index.yaml file from their metadata. Some information from
// the old index.yaml file is used to avoid making unnecessary changes,
// but for the most part this function enforces the idea that the
// index.yaml file should treat the charts' Chart.yaml files as the
// authoritative source of chart metadata.
func writeIndex() error {
	indexFilePath := filepath.Join(paths.GetRepoRoot(), indexFile)
	assetsDirectoryPath := filepath.Join(paths.GetRepoRoot(), repositoryAssetsDir)
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
			iconPath, err := icons.GetDownloadedIconPath(newChartVersion.Name)
			if err != nil {
				// TODO: return an error here instead of simply logging it.
				// Logged errors can be ignored; errors that prevent the user
				// from completing their task get fixed. But the errors in
				// rancher/partner-charts must be addressed before we can
				// do this.
				logrus.Errorf("failed to get downloaded icon path for chart %q version %q: %s", newChartVersion.Name, newChartVersion.Version, err)
			} else {
				newChartVersion.Icon = "file://" + iconPath
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

		upstreamYamlPath := filepath.Join(packageWrapper.Path, upstreamYamlFile)
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

// ensureIcons ensures that:
//  1. Each package has a valid icon file in assets/icons
//  2. Each chartVersion in index.yaml has its icon URL set to the local
//     path of the downloaded icon
func ensureIcons(c *cli.Context) error {
	currentPackage := os.Getenv(packageEnvVariable)

	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	for _, packageWrapper := range packageWrappers {
		if _, err := icons.GetDownloadedIconPath(packageWrapper.Name); err == nil {
			continue
		}
		existingCharts, err := loadExistingCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name)
		if err != nil {
			logrus.Errorf("failed to load existing charts for package %s: %s", packageWrapper.FullName(), err)
		}
		if len(existingCharts) == 0 {
			logrus.Errorf("found no existing charts for package %q", packageWrapper.FullName())
		}
		if _, err := icons.EnsureIconDownloaded(existingCharts[0].Metadata.Icon, packageWrapper.Name); err != nil {
			logrus.Errorf("failed to ensure icon downloaded for package %q: %s", packageWrapper.FullName(), err)
		}
	}

	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
}

// generateChanges will generate the changes for the packages based on the flags provided
// if auto or stage is true, it will write the index.yaml file if the chart has new updates
// the charts to be modified depends on the populatePackages function and their update status
// the changes will be applied on fetchUpstreams function
func generateChanges(auto bool) {
	currentPackage := os.Getenv(packageEnvVariable)
	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		logrus.Fatalf("failed to list packages: %s", err)
	}

	packageList := make(PackageList, 0, len(packageWrappers))
	for _, packageWrapper := range packageWrappers {
		if packageWrapper.UpstreamYaml.Deprecated {
			logrus.Warnf("Package %s is deprecated; skipping update", packageWrapper.FullName())
			continue
		}

		logrus.Debugf("Populating package from %s\n", packageWrapper.Path)
		updated, err := packageWrapper.Populate()
		if err != nil {
			logrus.Errorf("failed to populate %s: %s", packageWrapper.FullName(), err)
			continue
		}

		if len(packageWrapper.FetchVersions) == 0 {
			logrus.Infof("%s is up-to-date\n", packageWrapper.FullName())
		}
		for _, version := range packageWrapper.FetchVersions {
			logrus.Infof("\n  Package: %s\n  Source: %s\n  Version: %s\n  URL: %s  \n",
				packageWrapper.FullName(), packageWrapper.SourceMetadata.Source, version.Version, version.URLs[0])
		}

		if updated {
			packageList = append(packageList, packageWrapper)
		}
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
		err = commitChanges(packageList)
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
		packagesPath := filepath.Join(paths.GetRepoRoot(), repositoryPackagesDir)
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

// addFeaturedChart adds the "featured" annotation to a chart.
func addFeaturedChart(c *cli.Context) error {
	if len(c.Args()) != 2 {
		logrus.Fatalf("Please provide the chart name and featured number (1 - %d) as arguments\n", featuredMax)
	}
	featuredChart := c.Args().Get(0)
	inputIndex := c.Args().Get(1)
	featuredNumber, err := strconv.Atoi(inputIndex)
	if err != nil {
		return fmt.Errorf("failed to parse given index %q: %w", inputIndex, err)
	}
	if featuredNumber < 1 || featuredNumber > featuredMax {
		return fmt.Errorf("featured number must be between %d and %d\n", 1, featuredMax)
	}

	packageList, err := listPackageWrappers(featuredChart)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	packageWrapper := packageList[0]

	featuredVersions := getByAnnotation(annotationFeatured, inputIndex)
	if len(featuredVersions) > 0 {
		for chartName := range featuredVersions {
			logrus.Errorf("%s already featured at index %d\n", chartName, featuredNumber)
		}
	} else {
		vendor := packageWrapper.Vendor
		chartName := packageWrapper.Name
		if err := annotate(vendor, chartName, annotationFeatured, inputIndex, false, true); err != nil {
			return fmt.Errorf("failed to annotate %q: %w", packageWrapper.FullName(), err)
		}
		if err := writeIndex(); err != nil {
			return fmt.Errorf("failed to write index: %w", err)
		}
	}

	return nil
}

// removeFeaturedChart removes the "featured" annotation from a chart.
func removeFeaturedChart(c *cli.Context) error {
	if len(c.Args()) != 1 {
		logrus.Fatal("Please provide the chart name as argument")
	}
	featuredChart := c.Args().Get(0)

	packageList, err := listPackageWrappers(featuredChart)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	packageWrapper := packageList[0]

	vendor := packageWrapper.Vendor
	chartName := packageWrapper.Name
	if err := annotate(vendor, chartName, annotationFeatured, "", true, false); err != nil {
		return fmt.Errorf("failed to deannotate %q: %w", packageWrapper.FullName(), err)
	}

	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
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

// hideChart ensures each released version of a package has the "hidden"
// annotation set to "true". This hides the package in the Rancher UI.
func hideChart(c *cli.Context) error {
	if len(c.Args()) != 1 {
		logrus.Fatal("Must provide exactly one package name as argument")
	}
	currentPackage := c.Args().Get(0)

	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	packageWrapper := packageWrappers[0]

	// set Hidden: true in upstream.yaml
	packageWrapper.UpstreamYaml.Hidden = true
	if err := parse.WriteUpstreamYaml(packageWrapper.Path, *packageWrapper.UpstreamYaml); err != nil {
		return fmt.Errorf("failed to write upstream.yaml: %w", err)
	}

	vendor := packageWrapper.Vendor
	chartName := packageWrapper.Name
	if err := annotate(vendor, chartName, annotationHidden, "true", false, false); err != nil {
		return fmt.Errorf("failed to annotate package: %w", err)
	}
	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
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
	configYamlPath := path.Join(paths.GetRepoRoot(), configOptionsFile)
	configYaml, err := validate.ReadConfig(configYamlPath)
	if err != nil {
		logrus.Fatalf("failed to read %s: %s\n", configOptionsFile, err)
	}

	cloneDir, err := os.MkdirTemp("", "gitRepo")
	if err != nil {
		logrus.Fatal(err)
	}
	defer os.RemoveAll(cloneDir)

	err = validate.CloneRepo(configYaml.ValidateUpstreams[0].Url, configYaml.ValidateUpstreams[0].Branch, cloneDir)
	if err != nil {
		logrus.Fatal(err)
	}

	directoryComparison := validate.DirectoryComparison{}
	for _, dirPath := range []string{"assets"} {
		upstreamPath := path.Join(cloneDir, dirPath)
		updatePath := path.Join(paths.GetRepoRoot(), dirPath)
		if _, err := os.Stat(updatePath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in source. Skipping...", dirPath)
			continue
		}
		if _, err := os.Stat(upstreamPath); os.IsNotExist(err) {
			logrus.Infof("Directory '%s' not in upstream. Skipping...", dirPath)
			continue
		}
		newComparison, err := validate.CompareDirectories(upstreamPath, updatePath)
		if err != nil {
			logrus.Error(err)
		}
		directoryComparison.Merge(newComparison)
	}

	reportValidation(directoryComparison)

	logrus.Infof("Successfully validated\n  Upstream: %s\n  Branch: %s\n",
		configYaml.ValidateUpstreams[0].Url, configYaml.ValidateUpstreams[0].Branch)
}

func reportValidation(directoryComparison validate.DirectoryComparison) {
	repoRoot := paths.GetRepoRoot()

	if len(directoryComparison.Added) > 0 {
		outString := "Files Added:"
		for _, addedPath := range directoryComparison.Added {
			relativePath, err := filepath.Rel(repoRoot, addedPath)
			if err != nil {
				logrus.Fatalf("failed to get path of %s relative to %s", addedPath, repoRoot)
			}
			outString += fmt.Sprintf("\n - %s", relativePath)
		}
		logrus.Info(outString)
	}

	if len(directoryComparison.Removed) > 0 {
		outString := "Files Removed:"
		for _, removedPath := range directoryComparison.Removed {
			relativePath, err := filepath.Rel(repoRoot, removedPath)
			if err != nil {
				logrus.Fatalf("failed to get path of %s relative to %s", removedPath, repoRoot)
			}
			outString += fmt.Sprintf("\n - %s", relativePath)
		}
		logrus.Warn(outString)
	}

	if len(directoryComparison.Modified) > 0 {
		outString := "Files Modified:"
		for _, modifiedPath := range directoryComparison.Modified {
			relativePath, err := filepath.Rel(repoRoot, modifiedPath)
			if err != nil {
				logrus.Fatalf("failed to get path of %s relative to %s", modifiedPath, repoRoot)
			}
			outString += fmt.Sprintf("\n - %s", relativePath)
		}
		logrus.Fatal(outString)
	}
}

// cullCharts removes chart versions that are older than the passed number of
// days. Like many other subcommands, the PACKAGE environment variable can be
// used to work on a single package.
func cullCharts(c *cli.Context) error {
	currentPackage := os.Getenv(packageEnvVariable)
	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}

	// parse days argument
	rawDays := c.Args().Get(0)
	daysInt64, err := strconv.ParseInt(rawDays, 10, strconv.IntSize)
	if err != nil {
		return fmt.Errorf("failed to convert %q to integer: %w", rawDays, err)
	}
	days := int(daysInt64)

	_, newerChartVersions, err := getOlderAndNewerChartVersions(days)
	if err != nil {
		return fmt.Errorf("failed to get older and newer chart versions: %w", err)
	}

	skippedPackages := make([]string, 0, len(packageWrappers))
	for _, packageWrapper := range packageWrappers {
		logrus.Infof("culling %s", packageWrapper.FullName())
		existingCharts, err := loadExistingCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name)
		if err != nil {
			logrus.Errorf("failed to load existing charts for %q: %s", packageWrapper.FullName(), err)
			skippedPackages = append(skippedPackages, packageWrapper.FullName())
			continue
		}

		keptCharts := make([]*ChartWrapper, 0, len(existingCharts))
		for _, existingChart := range existingCharts {
			if slices.Contains(newerChartVersions[packageWrapper.Name], existingChart.Metadata.Version) {
				keptCharts = append(keptCharts, existingChart)
			}
		}
		if len(keptCharts) == 0 {
			logrus.Errorf("no versions of %s would remain; skipping...",
				packageWrapper.FullName())
			skippedPackages = append(skippedPackages, packageWrapper.FullName())
			continue
		}

		if err := writeCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name, keptCharts); err != nil {
			logrus.Errorf("failed to write charts for %q: %s", packageWrapper.FullName(), err)
			skippedPackages = append(skippedPackages, packageWrapper.FullName())
			continue
		}
	}

	if len(skippedPackages) > 0 {
		logrus.Errorf("skipped due to error:\n%s", strings.Join(skippedPackages, "\n"))
	}
	if len(skippedPackages) == len(packageWrappers) {
		logrus.Fatal("all packages skipped")
	}

	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
}

// getOlderAndNewerChartVersions splits the chartVersions in
// index.yaml into two groups: one that is more than days days old,
// and one that is less than days days old. They are returned as maps
// of chart name to slices of versions, one version per chartVersion.
// The older versions are the first return value and the newer
// versions are the second return value.
func getOlderAndNewerChartVersions(days int) (map[string][]string, map[string][]string, error) {
	indexYaml, err := repo.LoadIndexFile(indexFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read index file: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	olderVersions := make(map[string][]string)
	newerVersions := make(map[string][]string)
	for chartName, chartVersions := range indexYaml.Entries {
		for _, chartVersion := range chartVersions {
			if chartVersion.Created.After(cutoff) {
				newerVersions[chartName] = append(newerVersions[chartName], chartVersion.Version)
			} else {
				olderVersions[chartName] = append(olderVersions[chartName], chartVersion.Version)
			}
		}
	}

	return olderVersions, newerVersions, nil
}

func removePackage(c *cli.Context) error {
	if len(c.Args()) != 1 {
		return errors.New("must provide package name as argument")
	}
	currentPackage := c.Args().Get(0)

	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return fmt.Errorf("failed to list packages: %w", err)
	}
	packageWrapper := packageWrappers[0]

	if !force && !packageWrapper.UpstreamYaml.Deprecated {
		return fmt.Errorf("%s is not deprecated; use --force to force removal", packageWrapper.FullName())
	}

	removalPaths := []string{
		filepath.Join(paths.GetRepoRoot(), repositoryPackagesDir, packageWrapper.Vendor, packageWrapper.Name),
		filepath.Join(paths.GetRepoRoot(), repositoryChartsDir, packageWrapper.Vendor, packageWrapper.Name),
	}

	assetFiles, err := getExistingChartTgzFiles(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to list asset files for %s: %w", packageWrapper.FullName(), err)
	}
	removalPaths = append(removalPaths, assetFiles...)

	localIconPath, err := icons.GetDownloadedIconPath(packageWrapper.Name)
	if err != nil {
		logrus.Warnf("failed to get icon path for %s: %s", packageWrapper.FullName(), err)
	} else {
		removalPaths = append(removalPaths, localIconPath)
	}

	for _, removalPath := range removalPaths {
		if err := os.RemoveAll(removalPath); err != nil {
			logrus.Errorf("failed to remove %q: %s", removalPath, err)
		}
	}

	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
	}

	return nil
}

func deprecatePackage(c *cli.Context) error {
	if len(c.Args()) != 1 {
		return errors.New("must provide package name as argument")
	}
	currentPackage := c.Args().Get(0)

	packageWrappers, err := listPackageWrappers(currentPackage)
	if err != nil {
		return fmt.Errorf("failed to list package wrappers: %w", err)
	}
	packageWrapper := packageWrappers[0]

	// set Deprecated: true in upstream.yaml
	packageWrapper.UpstreamYaml.Deprecated = true
	upstreamYamlPath := filepath.Join(packageWrapper.Path, upstreamYamlFile)
	if err := upstreamyaml.Write(upstreamYamlPath, *packageWrapper.UpstreamYaml); err != nil {
		return fmt.Errorf("failed to write upstream.yaml: %w", err)
	}

	// set deprecated: true in each chart version's Chart.yaml
	chartWrappers, err := loadExistingCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to load existing charts: %w", err)
	}
	for _, chartWrapper := range chartWrappers {
		if !chartWrapper.Metadata.Deprecated {
			chartWrapper.Metadata.Deprecated = true
			chartWrapper.Modified = true
		}
	}
	if err := writeCharts(paths.GetRepoRoot(), packageWrapper.Vendor, packageWrapper.Name, chartWrappers); err != nil {
		return fmt.Errorf("failed to write charts: %w", err)
	}

	if err := writeIndex(); err != nil {
		return fmt.Errorf("failed to write index: %w", err)
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
			Name:   "ensure-icons",
			Usage:  "Ensure icons are downloaded and that chart versions in index.yaml use them",
			Action: ensureIcons,
		},
		{
			Name:      "cull",
			Usage:     "Remove versions of charts older than a number of days",
			Action:    cullCharts,
			ArgsUsage: "<days>",
		},
		{
			Name:      "remove",
			Usage:     "Remove a package and all of its associated chart versions",
			Action:    removePackage,
			ArgsUsage: "<package>",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:        "force, f",
					Usage:       "Skip check for package deprecation",
					Destination: &force,
				},
			},
		},
		{
			Name:      "deprecate",
			Usage:     "Deprecate a package and all of its associated chart versions",
			Action:    deprecatePackage,
			ArgsUsage: "<package>",
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}

}
