package validate

import (
	"testing"

	"github.com/rancher/partner-charts-ci/pkg/pkg"
	"github.com/stretchr/testify/assert"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/repo"
)

// TODO: this function was copied from icons_test.go, which is added in another
// PR. This can be deleted once the two branches are merged into main.
func generateIndex(t *testing.T) *repo.IndexFile {
	t.Helper()
	return &repo.IndexFile{
		Entries: map[string]repo.ChartVersions{
			"chart1": repo.ChartVersions{
				&repo.ChartVersion{
					Metadata: &chart.Metadata{
						Name:    "chart1",
						Version: "1.0.0",
						Icon:    "file://assets/icons/chart1.png",
					},
				},
			},
			"chart2": repo.ChartVersions{
				&repo.ChartVersion{
					Metadata: &chart.Metadata{
						Name:    "chart2",
						Version: "2.0.0",
						Icon:    "file://assets/icons/chart2.png",
					},
				},
			},
		},
	}
}

func TestMatchPackageNames(t *testing.T) {
	t.Run("should produce no error when name is present in index.yaml but not in packages/", func(t *testing.T) {
		indexYaml := generateIndex(t)
		packageWrappers := []pkg.PackageWrapper{
			{
				Name:   "chart1",
				Vendor: "vendor1",
			},
			{
				Name:   "chart2",
				Vendor: "vendor1",
			},
		}
		errors := matchPackageNames(indexYaml, packageWrappers)
		assert.Len(t, errors, 0)
	})

	t.Run("should produce error when name is present in index.yaml but not in packages/", func(t *testing.T) {
		indexYaml := generateIndex(t)
		packageWrappers := []pkg.PackageWrapper{
			{
				Name:   "chart1",
				Vendor: "vendor1",
			},
		}
		errors := matchPackageNames(indexYaml, packageWrappers)
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], `chart name "chart2" is present in index.yaml but not in packages/`)
	})

	t.Run("should produce error when name is present in packages/ but not in index.yaml", func(t *testing.T) {
		indexYaml := generateIndex(t)
		packageWrappers := []pkg.PackageWrapper{
			{
				Name:   "chart1",
				Vendor: "vendor1",
			},
			{
				Name:   "chart2",
				Vendor: "vendor1",
			},
			{
				Name:   "chart3",
				Vendor: "vendor1",
			},
		}
		errors := matchPackageNames(indexYaml, packageWrappers)
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], `chart name "chart3" is present in packages/ but not in index.yaml`)
	})
}
