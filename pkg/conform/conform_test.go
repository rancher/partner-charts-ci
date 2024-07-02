package conform

import (
	"testing"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/stretchr/testify/assert"
)

const testAnnotation = "test-annotation"

func TestMain(t *testing.T) {
	t.Run("AnnotateChart", func(t *testing.T) {
		t.Run("should not modify an existing annotation when override is false", func(t *testing.T) {
			firstValue := "testvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{
						testAnnotation: firstValue,
					},
				},
			}
			result := AnnotateChart(helmChart, testAnnotation, "newtestvalue", false)
			assert.False(t, result)
			assert.Equal(t, firstValue, helmChart.Metadata.Annotations[testAnnotation])
		})

		t.Run("should modify an existing annotation when override is true", func(t *testing.T) {
			firstValue := "testvalue"
			secondValue := "newtestvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{
						testAnnotation: firstValue,
					},
				},
			}
			result := AnnotateChart(helmChart, testAnnotation, secondValue, true)
			assert.True(t, result)
			assert.Equal(t, secondValue, helmChart.Metadata.Annotations[testAnnotation])
		})

		t.Run("should set an annotation that is not already set", func(t *testing.T) {
			testValue := "testvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{},
				},
			}
			result := AnnotateChart(helmChart, testAnnotation, testValue, true)
			assert.True(t, result)
			assert.Equal(t, testValue, helmChart.Metadata.Annotations[testAnnotation])
		})
	})

	t.Run("DeannotateChart", func(t *testing.T) {
		t.Run("should not change Chart.Metadata.Annotation if it is nil", func(t *testing.T) {
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{},
			}
			result := DeannotateChart(helmChart, testAnnotation, "")
			assert.False(t, result)
			assert.Nil(t, helmChart.Metadata.Annotations)
		})

		t.Run("should remove the annotation when an empty value is passed", func(t *testing.T) {
			testValue := "testvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{
						testAnnotation: testValue,
					},
				},
			}
			result := DeannotateChart(helmChart, testAnnotation, "")
			assert.True(t, result)
			_, ok := helmChart.Metadata.Annotations[testAnnotation]
			assert.False(t, ok)
		})

		t.Run("should remove the annotation when the exact value is passed", func(t *testing.T) {
			testValue := "testvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{
						testAnnotation: testValue,
					},
				},
			}
			result := DeannotateChart(helmChart, testAnnotation, testValue)
			assert.True(t, result)
			_, ok := helmChart.Metadata.Annotations[testAnnotation]
			assert.False(t, ok)
		})

		t.Run("should not remove the annotation when a non-empty non-matching value is passed", func(t *testing.T) {
			testValue := "testvalue"
			passedValue := "passedvalue"
			helmChart := &chart.Chart{
				Metadata: &chart.Metadata{
					Annotations: map[string]string{
						testAnnotation: testValue,
					},
				},
			}
			result := DeannotateChart(helmChart, testAnnotation, passedValue)
			assert.False(t, result)
			val, ok := helmChart.Metadata.Annotations[testAnnotation]
			assert.True(t, ok)
			assert.Equal(t, testValue, val)
		})
	})
}
