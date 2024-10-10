package validate

import (
	"os"

	"github.com/sirupsen/logrus"

	"sigs.k8s.io/yaml"
)

type ConfigurationYaml struct {
	ValidateUpstreams []ValidateUpstream `json:"validate"`
}

type ValidateUpstream struct {
	Url    string
	Branch string
}

func ReadConfig(configYamlPath string) (ConfigurationYaml, error) {
	upstreamYamlFile, err := os.ReadFile(configYamlPath)
	configYaml := ConfigurationYaml{}
	if err != nil {
		logrus.Debug(err)
	} else {
		err = yaml.Unmarshal(upstreamYamlFile, &configYaml)
	}

	return configYaml, err
}
