package validate

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/repo"
)

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

func TestValidateLoadedIcons(t *testing.T) {
	t.Run("should not return any errors when everything is correct", func(t *testing.T) {
		indexYaml := generateIndex(t)
		icons := []string{
			"assets/icons/chart1.png",
			"assets/icons/chart2.png",
		}
		errors := validateLoadedIcons(indexYaml, icons)
		assert.Len(t, errors, 0)
	})

	t.Run("should return errors when icons referred to in index.yaml are not present", func(t *testing.T) {
		indexYaml := generateIndex(t)
		icons := []string{
			"assets/icons/chart2.png",
		}
		errors := validateLoadedIcons(indexYaml, icons)
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], fmt.Sprintf("icon file %s for %s version %s does not exist", "assets/icons/chart1.png", "chart1", "1.0.0"))
	})

	t.Run("should return errors when icon files are not referred to in index.yaml", func(t *testing.T) {
		thirdIconFile := "assets/icons/chart3.png"
		indexYaml := generateIndex(t)
		icons := []string{
			"assets/icons/chart1.png",
			"assets/icons/chart2.png",
			thirdIconFile,
		}
		errors := validateLoadedIcons(indexYaml, icons)
		assert.Len(t, errors, 1)
		assert.ErrorContains(t, errors[0], fmt.Sprintf("icon file %s is not referenced in index.yaml", thirdIconFile))
	})
}
