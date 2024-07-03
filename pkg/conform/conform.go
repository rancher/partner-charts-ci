package conform

import (
	"fmt"
	"math"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/sirupsen/logrus"

	"helm.sh/helm/v3/pkg/chart"
)

var (
	PatchNumMultiplier = uint64(math.Pow10(2))
	MaxPatchNum        = PatchNumMultiplier - 1
)

func OverlayChartMetadata(helmChart *chart.Chart, overlay chart.Metadata) {
	if overlay.Name != "" {
		helmChart.Metadata.Name = overlay.Name
	}
	if overlay.Home != "" {
		helmChart.Metadata.Home = overlay.Home
	}
	if overlay.Sources != nil {
		helmChart.Metadata.Sources = append(helmChart.Metadata.Sources, overlay.Sources...)
	}
	if overlay.Version != "" {
		helmChart.Metadata.Version = overlay.Version
	}
	if overlay.Description != "" {
		helmChart.Metadata.Description = overlay.Description
	}
	if overlay.Keywords != nil {
		helmChart.Metadata.Keywords = append(helmChart.Metadata.Keywords, overlay.Keywords...)
	}
	if overlay.Maintainers != nil {
		helmChart.Metadata.Maintainers = append(helmChart.Metadata.Maintainers, overlay.Maintainers...)
	}
	if overlay.Icon != "" {
		helmChart.Metadata.Icon = overlay.Icon
	}
	if overlay.APIVersion != "" {
		helmChart.Metadata.APIVersion = overlay.APIVersion
	}
	if overlay.Condition != "" {
		helmChart.Metadata.Condition = overlay.Condition
	}
	if overlay.Tags != "" {
		helmChart.Metadata.Tags = overlay.Tags
	}
	if overlay.AppVersion != "" {
		helmChart.Metadata.AppVersion = overlay.AppVersion
	}
	if overlay.Deprecated {
		helmChart.Metadata.Deprecated = overlay.Deprecated
	}
	if overlay.Annotations != nil {
		for annotation, value := range overlay.Annotations {
			AnnotateChart(helmChart, annotation, value, true)
		}
	}
	/* Leaving in place, commented, to match upstream Helm metadata
	   Annotation 'catalog.cattle.io/kube-version' is prefered
	if overlay.KubeVersion != "" {
		helmChart.Metadata.KubeVersion = overlay.KubeVersion
	}
	*/
	if overlay.Dependencies != nil {
		helmChart.Metadata.Dependencies = append(helmChart.Metadata.Dependencies, overlay.Dependencies...)
	}
	if overlay.Type != "" {
		helmChart.Metadata.Type = overlay.Type
	}

}

func AnnotateChart(helmChart *chart.Chart, annotation, value string, override bool) bool {
	if helmChart.Metadata.Annotations == nil {
		helmChart.Metadata.Annotations = make(map[string]string)
	}
	currentValue, ok := helmChart.Metadata.Annotations[annotation]
	if !ok || (ok && currentValue != value && override) {
		logrus.Debugf("Adding annotation '%s: %s' to %s (%s)\n", annotation, value, helmChart.Name(), helmChart.Metadata.Version)
		helmChart.Metadata.Annotations[annotation] = value
		return true
	}
	return false
}

func DeannotateChart(helmChart *chart.Chart, annotation, value string) bool {
	modified := false
	removeAnnotation := false

	if helmChart.Metadata.Annotations == nil {
		return modified
	}

	if _, ok := helmChart.Metadata.Annotations[annotation]; ok {
		if value != "" {
			if helmChart.Metadata.Annotations[annotation] == value {
				logrus.Debugf("Removing annotation '%s: %s' from %s (%s)\n", annotation, value, helmChart.Name(), helmChart.Metadata.Version)
				removeAnnotation = true
			}
		} else {
			logrus.Debugf("Removing annotation '%s' from %s (%s)\n", annotation, helmChart.Name(), helmChart.Metadata.Version)
			removeAnnotation = true
		}
	}
	if removeAnnotation {
		delete(helmChart.Metadata.Annotations, annotation)
		modified = true
	}

	return modified
}

func ApplyChartAnnotations(helmChart *chart.Chart, annotations map[string]string, override bool) bool {
	modified := false
	if helmChart.Metadata.Annotations == nil {
		helmChart.Metadata.Annotations = make(map[string]string)
	}

	for annotation, value := range annotations {
		if AnnotateChart(helmChart, annotation, value, override) {
			modified = true
		}
	}

	return modified

}

func RemoveChartAnnotations(helmChart *chart.Chart, annotations map[string]string) bool {
	modified := false
	for annotation, value := range annotations {
		if DeannotateChart(helmChart, annotation, value) {
			modified = true
		}
	}

	return modified
}

func StripPackageVersion(chartVersion string) string {
	version, err := semver.NewVersion(chartVersion)
	if err != nil {
		logrus.Error(err)
	}

	if version.Patch() >= PatchNumMultiplier {
		packageVersion := version.Patch() % 2
		patchVersion := (version.Patch() - packageVersion) / PatchNumMultiplier
		split := strings.Split(version.String(), ".")
		split[2] = fmt.Sprintf("%d", patchVersion)
		version, err = semver.NewVersion(strings.Join(split, "."))
		if err != nil {
			logrus.Error(err)
		}
	}

	return version.String()
}

func GeneratePackageVersion(upstreamChartVersion string, packageVersion *int) (string, error) {
	if packageVersion != nil {
		chartVersion, err := semver.NewVersion(upstreamChartVersion)
		if err != nil {
			return "", err
		}

		if uint64(*packageVersion) >= MaxPatchNum {
			return "", fmt.Errorf("package version %d is greater than maximum of %d", *packageVersion, MaxPatchNum)
		}

		patchVersion := PatchNumMultiplier*chartVersion.Patch() + uint64(*packageVersion)

		split := strings.Split(chartVersion.String(), ".")
		split[2] = fmt.Sprintf("%d", patchVersion)
		chartVersion, err = semver.NewVersion(strings.Join(split, "."))
		if err != nil {
			return "", err
		}

		return chartVersion.String(), nil
	}

	chartVersion, err := semver.NewVersion(upstreamChartVersion)
	if err != nil {
		return "", err
	}

	return chartVersion.String(), nil
}
