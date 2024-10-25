package validate

import (
	p "github.com/rancher/partner-charts-ci/pkg/paths"
)

// A ValidationFunc is a function that checks one specific thing about the
// partner charts repository. If anything is wrong, it may return one or
// more errors explaining what is wrong.
type ValidationFunc func(paths p.Paths, configYaml ConfigurationYaml) []error

func Run(paths p.Paths, configYaml ConfigurationYaml) []error {
	validationErrors := []error{}
	validationFuncs := []ValidationFunc{
		preventReleasedChartModifications,
		preventDuplicatePackageNames,
		validatePackagesDirectory,
		validateIndexYamlAndPackagesDirNamesMatch,
	}
	for _, validationFunc := range validationFuncs {
		errors := validationFunc(paths, configYaml)
		validationErrors = append(validationErrors, errors...)
	}
	return validationErrors
}
