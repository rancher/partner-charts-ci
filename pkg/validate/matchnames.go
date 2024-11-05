package validate

import (
	"fmt"

	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/rancher/partner-charts-ci/pkg/pkg"

	"helm.sh/helm/v3/pkg/repo"
)

func validateIndexYamlAndPackagesDirNamesMatch(paths p.Paths, _ ConfigurationYaml) []error {
	indexYaml, err := repo.LoadIndexFile(paths.IndexYaml)
	if err != nil {
		return []error{fmt.Errorf("failed to read index.yaml: %s", err)}
	}
	packageWrappers, err := pkg.ListPackageWrappers(paths, "")
	if err != nil {
		return []error{fmt.Errorf("failed to list package wrappers: %w", err)}
	}
	return matchPackageNames(indexYaml, packageWrappers)
}

func matchPackageNames(indexYaml *repo.IndexFile, packageWrappers []pkg.PackageWrapper) []error {
	errors := make([]error, 0, len(packageWrappers))
	indexYamlNames := map[string]bool{}
	for chartName, _ := range indexYaml.Entries {
		indexYamlNames[chartName] = false
	}
	for _, packageWrapper := range packageWrappers {
		if _, ok := indexYamlNames[packageWrapper.Name]; ok {
			indexYamlNames[packageWrapper.Name] = true
		} else {
			error := fmt.Errorf("chart name %q is present in packages/ but not in index.yaml", packageWrapper.Name)
			errors = append(errors, error)
		}
	}
	for packageName, present := range indexYamlNames {
		if !present {
			error := fmt.Errorf("chart name %q is present in index.yaml but not in packages/", packageName)
			errors = append(errors, error)
		}
	}

	return errors
}
