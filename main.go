package main

import (
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

// PackageWrapper is a representation of relevant package metadata
type PackageWrapper struct {
	//Chart Display Name
	DisplayName string
	//Filtered subset of versions to-be-fetched
	FetchVersions repo.ChartVersions
	//Path stores the package path in current repository
	Path string
	//LatestStored stores the latest version of the chart currently in the repo
	LatestStored repo.ChartVersion
	//Chart name
	Name string
	//SourceMetadata represents metadata fetched from the upstream repository
	SourceMetadata *fetcher.ChartSourceMetadata
	//UpstreamYaml represents the values set in the package's upstream.yaml file
	UpstreamYaml *parse.UpstreamYaml
	//Chart vendor
	Vendor string
	//Formatted version of chart vendor
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
	upstreamYaml, err := parse.ParseUpstreamYaml(packageWrapper.Path)
	if err != nil {
		return false, fmt.Errorf("failed to parse upstream.yaml: %w", err)
	}
	packageWrapper.UpstreamYaml = &upstreamYaml

	sourceMetadata, err := generateChartSourceMetadata(*packageWrapper.UpstreamYaml)
	if err != nil {
		return false, err
	}

	packageWrapper.SourceMetadata = sourceMetadata
	packageWrapper.Name = sourceMetadata.Versions[0].Name
	packageWrapper.Vendor, packageWrapper.ParsedVendor = parseVendor(packageWrapper.UpstreamYaml.Vendor, packageWrapper.Name, packageWrapper.Path)

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

	if packageWrapper.UpstreamYaml.DisplayName != "" {
		packageWrapper.DisplayName = packageWrapper.UpstreamYaml.DisplayName
	} else {
		packageWrapper.DisplayName = packageWrapper.Name
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
		if err != nil {
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

func getRelativePath(packagePath string) string {
	packagePath = filepath.ToSlash(packagePath)
	packagesPath := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	return strings.TrimPrefix(packagePath, packagesPath)
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

// Cleans up ephemeral chart directory files from package prepare
func cleanPackage(packagePath string) error {
	packageName := strings.TrimPrefix(getRelativePath(packagePath), "/")
	logrus.Infof("Cleaning package %s\n", packageName)
	chartsPath := path.Join(packagePath, repositoryChartsDir)
	if err := os.RemoveAll(chartsPath); err != nil {
		return fmt.Errorf("failed to remove charts directory: %w", err)
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

func parseVendor(upstreamYamlVendor, chartName, packagePath string) (string, string) {
	var vendor, vendorPath string
	packagePath = filepath.ToSlash(packagePath)
	packageRelativePath := getRelativePath(packagePath)
	if len(strings.Split(packageRelativePath, "/")) > 2 {
		vendorPath = strings.TrimPrefix(filepath.Dir(packageRelativePath), "/")
	} else {
		vendorPath = strings.TrimPrefix(packageRelativePath, "/")
	}

	if upstreamYamlVendor != "" {
		vendor = upstreamYamlVendor
	} else if len(vendorPath) > 0 {
		vendor = vendorPath
	} else {
		vendor = chartName
	}

	parsedVendor := strings.ReplaceAll(strings.ToLower(vendor), " ", "-")

	return vendor, parsedVendor
}

// Prepares and standardizes chart, then returns loaded chart object
func initializeChart(packagePath string, sourceMetadata fetcher.ChartSourceMetadata, chartVersion repo.ChartVersion) (*chart.Chart, error) {
	logrus.Debugf("Preparing package from %s", packagePath)
	chartDirectoryPath := path.Join(packagePath, repositoryChartsDir)

	var chartWithoutOverlayFiles *chart.Chart
	var err error
	if sourceMetadata.Source == "Git" {
		chartWithoutOverlayFiles, err = fetcher.LoadChartFromGit(chartVersion.URLs[0], sourceMetadata.SubDirectory, sourceMetadata.Commit)
	} else {
		chartWithoutOverlayFiles, err = fetcher.LoadChartFromUrl(chartVersion.URLs[0])
	}
	if err != nil {
		return nil, err
	}

	err = conform.ExportChartDirectory(chartWithoutOverlayFiles, chartDirectoryPath)
	if err != nil {
		logrus.Error(err)
	}

	err = conform.ApplyOverlayFiles(packagePath)
	if err != nil {
		return nil, err
	}

	helmChart, err := loader.Load(chartDirectoryPath)
	if err != nil {
		return nil, err
	}

	helmChart.Metadata.Version = chartVersion.Version

	return helmChart, nil
}

func ApplyUpdates(packageWrapper PackageWrapper, writeChart bool) error {
	logrus.Debugf("Conforming package from %s\n", packageWrapper.Path)

	existingCharts, err := loadExistingCharts(packageWrapper.ParsedVendor, packageWrapper.Name)
	if err != nil {
		return fmt.Errorf("failed to load existing charts: %w", err)
	}

	// for new charts, convert repo.ChartVersions to *chart.Chart
	newCharts := make([]*chart.Chart, 0, len(packageWrapper.FetchVersions))
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
		newCharts = append(newCharts, newChart)
	}

	modifiedCharts, err := integrateCharts(packageWrapper, existingCharts, newCharts)
	if err != nil {
		return fmt.Errorf("failed to reconcile charts for package %q: %w", packageWrapper.Name, err)
	}

	if !writeChart {
		return nil
	}
	for _, modifiedChart := range modifiedCharts {
		// TODO actually write charts here
		fmt.Println(modifiedChart)
	}

	return nil
}

func loadExistingCharts(vendor string, packageName string) ([]*chart.Chart, error) {
	assetsPath := filepath.Join(getRepoRoot(), repositoryAssetsDir, vendor)
	tgzFiles, err := os.ReadDir(assetsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read dir %q: %w", assetsPath, err)
	}
	existingCharts := make([]*chart.Chart, 0, len(tgzFiles))
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
		existingChart, err := loader.LoadFile(tgzFile.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to load chart %q: %w", tgzFile.Name(), err)
		}
		existingCharts = append(existingCharts, existingChart)
	}
	return existingCharts, nil
}

// integrateCharts integrates new charts from upstream with any
// existing charts. It applies modifications to the new charts, and
// ensures that the state of all charts, both current and new, is
// correct. Should never modify an existing chart, except for in
// the special case of the "featured" annotation. Returns a slice
// containing charts that have been modified.
func integrateCharts(packageWrapper PackageWrapper, existingCharts, newCharts []*chart.Chart) ([]*chart.Chart, error) {
	modifiedCharts := make([]*chart.Chart, 0, len(existingCharts)+len(newCharts))
	modifiedCharts = append(modifiedCharts, newCharts...)

	for _, newChart := range newCharts {
		// TODO: add overlay files as in initializeCharts
		if err := addAnnotations(packageWrapper, newChart); err != nil {
			return nil, fmt.Errorf("failed to add annotations to chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
		if err := ensureIcon(packageWrapper, newChart); err != nil {
			return nil, fmt.Errorf("failed to ensure icon for chart %q version %q: %w", newChart.Name(), newChart.Metadata.Version, err)
		}
	}

	modifiedChartsFromFeatured, err := ensureFeaturedAnnotation(existingCharts, newCharts)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure featured annotation: %w", err)
	}
	modifiedCharts = append(modifiedCharts, modifiedChartsFromFeatured...)

	return modifiedCharts, nil
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

	conform.OverlayChartMetadata(helmChart, packageWrapper.UpstreamYaml.ChartYaml)

	if packageWrapper.UpstreamYaml.Namespace != "" {
		annotations[annotationNamespace] = packageWrapper.UpstreamYaml.Namespace
	}
	if helmChart.Metadata.KubeVersion != "" && packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
		annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
		helmChart.Metadata.KubeVersion = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
	} else if helmChart.Metadata.KubeVersion != "" {
		annotations[annotationKubeVersion] = helmChart.Metadata.KubeVersion
	} else if packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
		annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
	}

	if packageVersion := packageWrapper.UpstreamYaml.PackageVersion; packageVersion != 0 {
		generatedVersion, err := conform.GeneratePackageVersion(helmChart.Metadata.Version, &packageVersion, "")
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
// versions. Returns a slice of modified charts.
func ensureFeaturedAnnotation(existingCharts, newCharts []*chart.Chart) ([]*chart.Chart, error) {
	modifiedCharts := make([]*chart.Chart, 0, len(existingCharts)+len(newCharts))

	// get current value of featured annotation
	featuredAnnotationValue := ""
	for _, existingChart := range existingCharts {
		val, ok := existingChart.Metadata.Annotations[annotationFeatured]
		if !ok {
			continue
		}
		if featuredAnnotationValue != "" && featuredAnnotationValue != val {
			return nil, fmt.Errorf("found two different values for featured annotation %q and %q", featuredAnnotationValue, val)
		}
	}
	if featuredAnnotationValue == "" {
		// the chart is not featured
		return nil, nil
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
	if conform.AnnotateChart(lastNewChart, annotationFeatured, featuredAnnotationValue, true) {
		modifiedCharts = append(modifiedCharts, lastNewChart)
	}

	// Ensure featured annotation is not present on existing charts. We don't
	// need to worry about other new charts because they will not have the
	// featured annotation.
	for _, existingChart := range existingCharts {
		if conform.DeannotateChart(existingChart, annotationFeatured, "") {
			modifiedCharts = append(modifiedCharts, existingChart)
		}
	}

	return modifiedCharts, nil
}

// Mutates chart with necessary alterations for repository. Only writes
// the chart to disk if writeChart is true.
func conformPackage(packageWrapper PackageWrapper) error {
	var err error
	logrus.Debugf("Conforming package from %s\n", packageWrapper.Path)
	for _, chartVersion := range packageWrapper.FetchVersions {
		logrus.Debugf("Conforming package %s (%s)\n", chartVersion.Name, chartVersion.Version)
		helmChart, err := initializeChart(
			packageWrapper.Path,
			*packageWrapper.SourceMetadata,
			*chartVersion,
		)
		if err != nil {
			return err
		}
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

		conform.OverlayChartMetadata(helmChart, packageWrapper.UpstreamYaml.ChartYaml)

		if val, ok := getByAnnotation(annotationFeatured, "")[packageWrapper.Name]; ok {
			logrus.Debugf("Migrating featured annotation to latest version %s\n", packageWrapper.Name)
			featuredIndex := val[0].Annotations[annotationFeatured]
			err := annotate(packageWrapper.ParsedVendor, packageWrapper.LatestStored.Name, annotationFeatured, "", true, false)
			if err != nil {
				return fmt.Errorf("failed to annotate package: %w", err)
			}
			if err = writeIndex(); err != nil {
				return fmt.Errorf("failed to write index: %w", err)
			}
			annotations[annotationFeatured] = featuredIndex
		}

		if packageWrapper.UpstreamYaml.Namespace != "" {
			annotations[annotationNamespace] = packageWrapper.UpstreamYaml.Namespace
		}
		if helmChart.Metadata.KubeVersion != "" && packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
			annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
			helmChart.Metadata.KubeVersion = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
		} else if helmChart.Metadata.KubeVersion != "" {
			annotations[annotationKubeVersion] = helmChart.Metadata.KubeVersion
		} else if packageWrapper.UpstreamYaml.ChartYaml.KubeVersion != "" {
			annotations[annotationKubeVersion] = packageWrapper.UpstreamYaml.ChartYaml.KubeVersion
		}

		if packageVersion := packageWrapper.UpstreamYaml.PackageVersion; packageVersion != 0 {
			helmChart.Metadata.Version, err = conform.GeneratePackageVersion(helmChart.Metadata.Version, &packageVersion, "")
			if err != nil {
				logrus.Error(err)
			}
		}

		conform.ApplyChartAnnotations(helmChart, annotations, false)

		// write chart
		err = cleanPackage(packageWrapper.Path)
		if err != nil {
			logrus.Debug(err)
		}
		assetsPath := filepath.Join(
			getRepoRoot(),
			repositoryAssetsDir,
			packageWrapper.ParsedVendor)
		chartsPath := filepath.Join(
			getRepoRoot(),
			repositoryChartsDir,
			packageWrapper.ParsedVendor,
			helmChart.Metadata.Name)
		if err := os.RemoveAll(chartsPath); err != nil {
			return fmt.Errorf("failed to remove chartsPath %q: %w", chartsPath, err)
		}
		err = saveChart(helmChart, assetsPath, chartsPath)
		if err != nil {
			return err
		}

	}

	return err
}

// Saves chart to disk as asset gzip and directory
func saveChart(helmChart *chart.Chart, assetsPath, chartsPath string) error {

	logrus.Debugf("Exporting chart assets to %s\n", assetsPath)
	assetFile, err := chartutil.Save(helmChart, assetsPath)
	if err != nil {
		return fmt.Errorf("failed to save chart %q version %q: %w", helmChart.Name(), helmChart.Metadata.Version, err)
	}

	err = conform.Gunzip(assetFile, chartsPath)
	if err != nil {
		logrus.Error(err)
	}

	logrus.Debugf("Exporting chart to %s\n", chartsPath)
	err = conform.ExportChartDirectory(helmChart, chartsPath)
	if err != nil {
		return err
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

// Writes out modified index file
func writeIndex() error {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	if _, err := os.Stat(indexFilePath); os.IsNotExist(err) {
		err = repo.NewIndexFile().WriteFile(indexFilePath, 0644)
		if err != nil {
			return err
		}
	}

	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	if err != nil {
		return err
	}

	assetsDirectoryPath := filepath.Join(getRepoRoot(), repositoryAssetsDir)
	newHelmIndexYaml, err := repo.IndexDirectory(assetsDirectoryPath, repositoryAssetsDir)
	if err != nil {
		return err
	}
	helmIndexYaml.Merge(newHelmIndexYaml)
	helmIndexYaml.SortEntries()

	err = helmIndexYaml.WriteFile(indexFilePath, 0644)
	if err != nil {
		return err
	}

	return nil
}

// Generates list of package paths with upstream yaml available
func generatePackageList(currentPackage string) PackageList {
	packageDirectory := filepath.Join(getRepoRoot(), repositoryPackagesDir)
	packageMap, err := parse.ListPackages(packageDirectory, currentPackage)
	if err != nil {
		logrus.Error(err)
	}

	// get sorted list of package names
	packageNames := make([]string, 0, len(packageMap))
	for packageName := range packageMap {
		packageNames = append(packageNames, packageName)
	}
	sort.Strings(packageNames)

	// construct list of PackageWrappers
	packageList := make(PackageList, 0, len(packageNames))
	for _, packageName := range packageNames {
		packageWrapper := PackageWrapper{
			Path: packageMap[packageName],
		}
		packageList = append(packageList, packageWrapper)
	}

	return packageList
}

// Populates list of package wrappers, handles manual and automatic variation
// If print, function will print information during processing
func populatePackages(currentPackage string, onlyUpdates bool, onlyLatest bool, print bool) (PackageList, error) {
	packageList := make(PackageList, 0)
	for _, packageWrapper := range generatePackageList(currentPackage) {
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

// overrideIcons will get the package list and override the icon field in the index.yaml file with the downloaded icons.
// It will also test if the icons are correctly overridden and if the index.yaml file is correctly written.
// If the test fails, it will return an error and the user should check the logs for more information.
// It will only work if the downloadIcons function was previously executed at some point in time.
// The function will not download the icons, it will only override the icon field in the index.yaml file.
// Before overriding the icons, it will check if the necessary conditions are met at parsePackageListToPackageIconList() function, if not it will skip the package.
func overrideIcons() {
	currentPackage := os.Getenv(packageEnvVariable)
	iconOverride := true
	icons.CheckFilesStructure() // stop execution if file structure is not correct

	// populate all possible packages
	packageList, err := populatePackages(currentPackage, false, false, false)
	if err != nil {
		logrus.Fatal(err)
	}

	// parse only the packages that have the necessary conditions for icon override
	packageIconList := parsePackageListToPackageIconList(packageList)

	err = overwriteIndexIconsAndTestChanges(packageIconList)
	if err != nil {
		logrus.Errorf("Failed to overwrite index icons: %v", err)
	}

	err = commitChanges(packageList, iconOverride)
	if err != nil {
		logrus.Fatal(err)
	}
}

// parsePackageListToPackageIconList will parse the PackageList to PackageIconList
// and check if the necessary override icon conditions are met
func parsePackageListToPackageIconList(packageList PackageList) icons.PackageIconList {
	var packageIconList icons.PackageIconList
	for _, pkg := range packageList {

		// check conditions for icon override and avoid panics
		iconURL, err := icons.GetDownloadedIconPath(pkg.Name)
		if err != nil {
			logrus.Errorf("failed to get downloaded icon path: %s", err)
			continue
		}

		pkgIcon := icons.ParsePackageToPackageIconOverride(pkg.Name, pkg.Path, iconURL)
		packageIconList = append(packageIconList, pkgIcon)
	}
	return packageIconList
}

// overwriteIndexIconsAndTestChanges will overwrite the index.yaml icon fields with the new downloaded icons path
func overwriteIndexIconsAndTestChanges(packageIconList icons.PackageIconList) error {
	indexFilePath := filepath.Join(getRepoRoot(), indexFile)
	helmIndexYaml, err := repo.LoadIndexFile(indexFilePath)
	if err != nil {
		return fmt.Errorf("failed to load index file: %w", err)
	}
	assetsDirectoryPath := filepath.Join(getRepoRoot(), repositoryAssetsDir)
	newHelmIndexYaml, err := repo.IndexDirectory(assetsDirectoryPath, repositoryAssetsDir)
	if err != nil {
		return err
	}
	helmIndexYaml.Merge(newHelmIndexYaml)
	helmIndexYaml.SortEntries()

	icons.OverrideIconValues(helmIndexYaml, packageIconList)

	err = helmIndexYaml.WriteFile(indexFilePath, 0644)
	if err != nil {
		return err
	}

	updatedHelmIndexFile, _ := repo.LoadIndexFile(indexFilePath)

	return icons.ValidateIconsAndIndexYaml(packageIconList, updatedHelmIndexFile)
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
		if err := conformPackage(packageWrapper); err != nil {
			logrus.Error(err)
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
func listPackages(c *cli.Context) {
	packageList := generatePackageList(os.Getenv(packageEnvVariable))
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
}

// CLI function call - Appends annotaion to feature chart in Rancher UI
func addFeaturedChart(c *cli.Context) {
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

	packageList := generatePackageList(featuredChart)
	if len(packageList) == 0 {
		logrus.Fatalf("Package '%s' not available\n", featuredChart)
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

// CLI function call - Cleans package object(s)
func cleanCharts(c *cli.Context) {
	packageList := generatePackageList(os.Getenv(packageEnvVariable))
	for _, packageWrapper := range packageList {
		err := cleanPackage(packageWrapper.Path)
		if err != nil {
			logrus.Error(err)
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
	icons := c.Bool("icons")
	generateChanges(true)
	if icons {
		overrideIcons()
	}
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
			Name:   "clean",
			Usage:  "Clean up ephemeral chart directory",
			Action: cleanCharts,
		},
		{
			Name:   "auto",
			Usage:  "Generate and commit changes",
			Action: autoUpdate,
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "icons",
					Usage: "override icons in index.yaml if true",
				},
			},
		},
		{
			Name:   "stage",
			Usage:  "Stage all changes. Does not commit",
			Action: stageChanges,
			Hidden: true, // Hidden because this subcommand does not execute overrideIcons
			// that is necessary in the current release process,
			// this should not be executed and pushed to production
			// otherwise we will not have the icons updated at index.yaml.
			// You should use the auto command instead.
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
