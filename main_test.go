package main

import (
	"path/filepath"
	"testing"

	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/rancher/partner-charts-ci/pkg/pkg"
	"github.com/rancher/partner-charts-ci/pkg/upstreamyaml"
	"github.com/stretchr/testify/assert"

	"helm.sh/helm/v3/pkg/chart"
)

func getPaths(t *testing.T, repoRoot string) p.Paths {
	t.Helper()
	assets := filepath.Join(repoRoot, "assets")
	return p.Paths{
		RepoRoot:          repoRoot,
		Assets:            assets,
		Charts:            filepath.Join(repoRoot, "charts"),
		ConfigurationYaml: filepath.Join(repoRoot, "configuration.yaml"),
		Icons:             filepath.Join(assets, "icons"),
		IndexYaml:         filepath.Join(repoRoot, "index.yaml"),
		Packages:          filepath.Join(repoRoot, "packages"),
	}
}

func TestMain(t *testing.T) {
	t.Run("applyOverlayFiles", func(t *testing.T) {
		t.Run("should add files that do not already exist", func(t *testing.T) {
			filename := "file1.txt"
			overlayFiles := map[string][]byte{
				filename: []byte("this is file 1"),
			}
			helmChart := &chart.Chart{}
			if err := applyOverlayFiles(overlayFiles, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			found := false
			for _, file := range helmChart.Files {
				if file.Name == filename {
					found = true
					assert.Equal(t, overlayFiles[filename], file.Data)
				}
			}
			assert.True(t, found)
		})

		t.Run("should overwrite existing files", func(t *testing.T) {
			filename := "file1.txt"
			filedata := []byte("this is file 1")
			overlayFiles := map[string][]byte{
				filename: filedata,
			}
			helmChart := &chart.Chart{
				Files: []*chart.File{
					{
						Name: filename,
						Data: []byte("these are different contents"),
					},
				},
			}
			if err := applyOverlayFiles(overlayFiles, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			found := false
			for _, file := range helmChart.Files {
				if file.Name == filename {
					found = true
					assert.Equal(t, overlayFiles[filename], file.Data)
				}
			}
			assert.True(t, found)
			assert.Equal(t, 1, len(helmChart.Files))
		})
	})

	t.Run("addAnnotations", func(t *testing.T) {
		t.Run("should set auto-install annotation properly", func(t *testing.T) {
			for _, autoInstall := range []string{"", "some-chart"} {
				packageWrapper := pkg.PackageWrapper{
					UpstreamYaml: &upstreamyaml.UpstreamYaml{
						AutoInstall: autoInstall,
					},
				}
				helmChart := &chart.Chart{
					Metadata: &chart.Metadata{
						Dependencies: []*chart.Dependency{},
					},
				}
				if err := addAnnotations(packageWrapper, helmChart); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				value, ok := helmChart.Metadata.Annotations[annotationAutoInstall]
				assert.Equal(t, autoInstall != "", ok)
				if autoInstall != "" {
					assert.Equal(t, autoInstall, value)
				}
			}
		})

		t.Run("should set experimental annotation properly", func(t *testing.T) {
			for _, experimental := range []bool{false, true} {
				packageWrapper := pkg.PackageWrapper{
					UpstreamYaml: &upstreamyaml.UpstreamYaml{
						Experimental: experimental,
					},
				}
				helmChart := &chart.Chart{
					Metadata: &chart.Metadata{
						Dependencies: []*chart.Dependency{},
					},
				}
				if err := addAnnotations(packageWrapper, helmChart); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				value, ok := helmChart.Metadata.Annotations[annotationExperimental]
				assert.Equal(t, experimental, ok)
				if experimental {
					assert.Equal(t, "true", value)
				}
			}
		})

		t.Run("should set hidden annotation properly", func(t *testing.T) {
			for _, hidden := range []bool{false, true} {
				packageWrapper := pkg.PackageWrapper{
					UpstreamYaml: &upstreamyaml.UpstreamYaml{
						Hidden: hidden,
					},
				}
				helmChart := &chart.Chart{
					Metadata: &chart.Metadata{
						Dependencies: []*chart.Dependency{},
					},
				}
				if err := addAnnotations(packageWrapper, helmChart); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				value, ok := helmChart.Metadata.Annotations[annotationHidden]
				assert.Equal(t, hidden, ok)
				if hidden {
					assert.Equal(t, "true", value)
				}
			}
		})

		t.Run("should always set certified annotation", func(t *testing.T) {
			packageWrapper := pkg.PackageWrapper{
				UpstreamYaml: &upstreamyaml.UpstreamYaml{},
			}
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Dependencies: []*chart.Dependency{},
				},
			}
			if err := addAnnotations(packageWrapper, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			value, ok := helmChart.Metadata.Annotations[annotationCertified]
			assert.True(t, ok)
			assert.Equal(t, "partner", value)
		})

		t.Run("should always set display-name annotation", func(t *testing.T) {
			displayName := "Display Name"
			packageWrapper := pkg.PackageWrapper{
				DisplayName:  displayName,
				UpstreamYaml: &upstreamyaml.UpstreamYaml{},
			}
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Dependencies: []*chart.Dependency{},
				},
			}
			if err := addAnnotations(packageWrapper, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			value, ok := helmChart.Metadata.Annotations[annotationDisplayName]
			assert.True(t, ok)
			assert.Equal(t, displayName, value)
		})

		t.Run("should set release-name annotation to package name when ReleaseName is not set", func(t *testing.T) {
			packageName := "package-name"
			packageWrapper := pkg.PackageWrapper{
				Name:         packageName,
				UpstreamYaml: &upstreamyaml.UpstreamYaml{},
			}
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Dependencies: []*chart.Dependency{},
				},
			}
			if err := addAnnotations(packageWrapper, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			value, ok := helmChart.Metadata.Annotations[annotationReleaseName]
			assert.True(t, ok)
			assert.Equal(t, packageName, value)
		})

		t.Run("should set release-name annotation to ReleaseName when ReleaseName is set", func(t *testing.T) {
			releaseName := "Release Name"
			packageWrapper := pkg.PackageWrapper{
				UpstreamYaml: &upstreamyaml.UpstreamYaml{
					ReleaseName: releaseName,
				},
			}
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Dependencies: []*chart.Dependency{},
				},
			}
			if err := addAnnotations(packageWrapper, helmChart); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			value, ok := helmChart.Metadata.Annotations[annotationReleaseName]
			assert.True(t, ok)
			assert.Equal(t, releaseName, value)
		})

		t.Run("should set namespace annotation properly", func(t *testing.T) {
			for _, namespace := range []string{"", "test-namespace"} {
				packageWrapper := pkg.PackageWrapper{
					UpstreamYaml: &upstreamyaml.UpstreamYaml{
						Namespace: namespace,
					},
				}
				helmChart := &chart.Chart{
					Metadata: &chart.Metadata{
						Dependencies: []*chart.Dependency{},
					},
				}
				if err := addAnnotations(packageWrapper, helmChart); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				value, ok := helmChart.Metadata.Annotations[annotationNamespace]
				assert.Equal(t, namespace != "", ok)
				if namespace != "" {
					assert.Equal(t, namespace, value)
				}
			}
		})

		t.Run("should set kube-version annotation properly", func(t *testing.T) {
			testCases := []struct {
				CurrentKubeVersion      string
				UpstreamYamlKubeVersion string
				ExpectedValue           string
			}{
				{
					CurrentKubeVersion:      "currentKubeVersion",
					UpstreamYamlKubeVersion: "upstreamYamlKubeVersion",
					ExpectedValue:           "upstreamYamlKubeVersion",
				},
				{
					CurrentKubeVersion:      "",
					UpstreamYamlKubeVersion: "upstreamYamlKubeVersion",
					ExpectedValue:           "upstreamYamlKubeVersion",
				},
				{
					CurrentKubeVersion:      "currentKubeVersion",
					UpstreamYamlKubeVersion: "",
					ExpectedValue:           "currentKubeVersion",
				},
				{
					CurrentKubeVersion:      "",
					UpstreamYamlKubeVersion: "",
					ExpectedValue:           "",
				},
			}
			for _, testCase := range testCases {
				packageWrapper := pkg.PackageWrapper{
					UpstreamYaml: &upstreamyaml.UpstreamYaml{
						ChartMetadata: chart.Metadata{
							KubeVersion: testCase.UpstreamYamlKubeVersion,
						},
					},
				}
				helmChart := &chart.Chart{
					Metadata: &chart.Metadata{
						KubeVersion:  testCase.CurrentKubeVersion,
						Dependencies: []*chart.Dependency{},
					},
				}
				if err := addAnnotations(packageWrapper, helmChart); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				value, ok := helmChart.Metadata.Annotations[annotationKubeVersion]
				if testCase.ExpectedValue == "" {
					assert.False(t, ok)
					continue
				}
				assert.True(t, ok)
				assert.Equal(t, testCase.ExpectedValue, value)
			}
		})
	})

	t.Run("ensureFeaturedAnnotation", func(t *testing.T) {
		t.Run("should return nil, nil when featured annotation not present on existing charts", func(t *testing.T) {
			existingCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart1",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart2",
						},
					},
				},
			}
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "newChart1",
						},
					},
				},
			}
			if err := ensureFeaturedAnnotation(existingCharts, newCharts); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			for _, existingChart := range existingCharts {
				assert.False(t, existingChart.Modified)
			}
			for _, newChart := range newCharts {
				assert.False(t, newChart.Modified)
			}
		})

		t.Run("should return error when there are two featured annotations of differing values in existing charts", func(t *testing.T) {
			existingCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart1",
							Annotations: map[string]string{
								annotationFeatured: "1",
							},
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart2",
							Annotations: map[string]string{
								annotationFeatured: "2",
							},
						},
					},
				},
			}
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "newChart1",
						},
					},
				},
			}
			err := ensureFeaturedAnnotation(existingCharts, newCharts)
			assert.NotNil(t, err)
			assert.Contains(t, err.Error(), "found two different values for featured annotation")
		})

		t.Run("should modify charts correctly and return modified charts", func(t *testing.T) {
			featuredAnnotationValue := "2"
			existingCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart1",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "existingChart2",
							Annotations: map[string]string{
								annotationFeatured: featuredAnnotationValue,
							},
						},
					},
				},
			}
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "newChart1",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							Name: "newChart2",
						},
					},
				},
			}

			if err := ensureFeaturedAnnotation(existingCharts, newCharts); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			// assert that annotations are set correctly
			for _, existingChart := range existingCharts {
				_, ok := existingChart.Metadata.Annotations[annotationFeatured]
				assert.False(t, ok)
			}
			for _, newChart := range newCharts[0 : len(newCharts)-1] {
				_, ok := newChart.Metadata.Annotations[annotationFeatured]
				assert.False(t, ok)
			}
			val, ok := newCharts[len(newCharts)-1].Metadata.Annotations[annotationFeatured]
			assert.True(t, ok)
			assert.Equal(t, featuredAnnotationValue, val)

			// assert that Modified property is set correctly
			for _, chartWrapper := range []*ChartWrapper{newCharts[1], existingCharts[1]} {
				assert.True(t, chartWrapper.Modified)
			}
			for _, chartWrapper := range []*ChartWrapper{newCharts[0], existingCharts[0]} {
				assert.False(t, chartWrapper.Modified)
			}
		})
	})

	t.Run("loadExistingCharts", func(t *testing.T) {
		t.Run("should sort charts in descending chart version order", func(t *testing.T) {
			vendor := "f5"
			packageName := "nginx-ingress"
			repoRoot := filepath.Join("testdata", "loadExistingCharts")
			paths := getPaths(t, repoRoot)
			chartWrappers, err := loadExistingCharts(paths, vendor, packageName)
			if err != nil {
				t.Fatalf("unexpected error: %s", err)
			}

			// should only parse charts with the right name
			assert.Equal(t, len(chartWrappers), 4)
			for _, chartWrapper := range chartWrappers {
				assert.Equal(t, chartWrapper.Name(), "nginx-ingress")
			}

			// should sort charts properly
			assert.Equal(t, chartWrappers[0].Metadata.Version, "1.3.1")
			assert.Equal(t, chartWrappers[1].Metadata.Version, "1.2.0")
			assert.Equal(t, chartWrappers[2].Metadata.Version, "1.1.3")
			assert.Equal(t, chartWrappers[3].Metadata.Version, "1.0.2")
		})
	})

	t.Run("writeCharts", func(t *testing.T) {
		t.Run("should add charts that do not exist on disk", func(t *testing.T) {
			vendor := "testVendor"
			chartName := "testChart"
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "2.3.4",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "1.2.3",
						},
					},
				},
			}
			paths := getPaths(t, t.TempDir())
			if err := writeCharts(paths, vendor, chartName, newCharts); err != nil {
				t.Fatalf("unexpected error in writeCharts: %s", err)
			}
			chartsFromDisk, err := loadExistingCharts(paths, vendor, chartName)
			if err != nil {
				t.Fatalf("unexpected error in loadExistingCharts: %s", err)
			}
			assert.Equal(t, len(newCharts), len(chartsFromDisk))
			for index := range newCharts {
				assert.Equal(t, newCharts[index].Metadata, chartsFromDisk[index].Metadata)
			}
		})

		t.Run("should delete charts that are present on disk but not passed in", func(t *testing.T) {
			vendor := "testVendor"
			chartName := "testChart"
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "3.4.5",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "2.3.4",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "1.2.3",
						},
					},
				},
			}
			paths := getPaths(t, t.TempDir())
			if err := writeCharts(paths, vendor, chartName, newCharts); err != nil {
				t.Fatalf("unexpected error in first writeCharts call: %s", err)
			}
			if err := writeCharts(paths, vendor, chartName, newCharts[0:2]); err != nil {
				t.Fatalf("unexpected error in second writeCharts call: %s", err)
			}
			chartsFromDisk, err := loadExistingCharts(paths, vendor, chartName)
			if err != nil {
				t.Fatalf("unexpected error in loadExistingCharts: %s", err)
			}
			assert.Equal(t, 2, len(chartsFromDisk))
			for index := range chartsFromDisk {
				assert.Equal(t, newCharts[index].Metadata, chartsFromDisk[index].Metadata)
			}
		})

		t.Run("should modify charts only when PackageWrapper.Modified is true", func(t *testing.T) {
			vendor := "testVendor"
			chartName := "testChart"
			annotationKey := "testAnnotation"
			annotationValue := "testAnnotationValue"
			newCharts := []*ChartWrapper{
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "2.3.4",
						},
					},
				},
				{
					Chart: &chart.Chart{
						Metadata: &chart.Metadata{
							APIVersion: "v2",
							Name:       chartName,
							Version:    "1.2.3",
						},
					},
				},
			}
			paths := getPaths(t, t.TempDir())
			if err := writeCharts(paths, vendor, chartName, newCharts); err != nil {
				t.Fatalf("unexpected error in first call of writeCharts: %s", err)
			}
			chartsFromDisk, err := loadExistingCharts(paths, vendor, chartName)
			if err != nil {
				t.Fatalf("unexpected error in first call of loadExistingCharts: %s", err)
			}

			// add annotation to both charts, but set Modified only on first chart
			for _, chartFromDisk := range chartsFromDisk {
				chartFromDisk.Metadata.Annotations = map[string]string{
					annotationKey: annotationValue,
				}
			}
			chartsFromDisk[0].Modified = true

			if err := writeCharts(paths, vendor, chartName, chartsFromDisk); err != nil {
				t.Fatalf("unexpected error in second call of writeCharts: %s", err)
			}
			newChartsFromDisk, err := loadExistingCharts(paths, vendor, chartName)
			if err != nil {
				t.Fatalf("unexpected error in second call of loadExistingCharts: %s", err)
			}

			assert.Equal(t, len(newCharts), len(newChartsFromDisk))
			value, ok := newChartsFromDisk[0].Metadata.Annotations[annotationKey]
			assert.True(t, ok)
			assert.Equal(t, annotationValue, value)
			assert.Nil(t, newChartsFromDisk[1].Metadata.Annotations)
		})
	})
}
