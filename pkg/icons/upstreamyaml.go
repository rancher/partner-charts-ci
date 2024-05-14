package icons

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// CreateOverrideUpstreamYamlMetadata will create a new or edit an existing upstream.yaml file
// under packages/<chart>/upstream.yaml that will be used to overlay icons data on the index.yaml
func CreateOverrideUpstreamYamlMetadata(downloadedIcons PackageIconMap) int {
	logrus.Info("Starting to create upstream.yaml ChartMetadata - icon")
	var counter int
	for _, objIconOverride := range downloadedIcons {
		upstreamFilePath := fmt.Sprintf("%s/upstream.yaml", objIconOverride.Path)
		exists := Exists(upstreamFilePath)

		if exists {
			err := writeChartMetadata(upstreamFilePath, objIconOverride.Icon)
			if err != nil {
				logrus.Errorf("Failed to write chart metadata to UpstreamYaml at: %s", upstreamFilePath)
				continue
			}
			counter++
		} else {
			err := createUpstreamYaml(upstreamFilePath)
			if err != nil {
				logrus.Errorf("Failed to create UpstreamYaml at: %s", upstreamFilePath)
				continue
			}
			err = writeChartMetadata(upstreamFilePath, objIconOverride.Icon)
			if err != nil {
				logrus.Errorf("Failed to write chart metadata to UpstreamYaml at: %s", upstreamFilePath)
				continue
			}
			counter++
		}
	}
	return counter
}

func createUpstreamYaml(file string) error {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		_, err := os.Create(file)
		if err != nil {
			logrus.Errorf("File creation failed: %v", err)
			return err
		}
	}
	return nil
}

// writeChartMetadata will write the icon to the upstream.yaml file
func writeChartMetadata(file, icon string) error {
	logrus.Infof("Writing icon to upstream.yaml file: %s; icon: %s", file, icon)
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		logrus.Infof("yamlFile.Get err   #%v ", err)
		return err
	}

	data := make(map[interface{}]interface{})

	err = yaml.Unmarshal(yamlFile, &data)
	if err != nil {
		logrus.Errorf("Unmarshal: %v", err)
		return err
	}

	// Check if ChartMetadata exists, if not create it
	chartMetadata, ok := data["ChartMetadata"].(map[interface{}]interface{})
	if !ok {
		chartMetadata = make(map[interface{}]interface{})
	}
	chartMetadata["icon"] = icon
	data["ChartMetadata"] = chartMetadata

	d, err := yaml.Marshal(&data)
	if err != nil {
		logrus.Errorf("Marshal: %v", err)
		return err
	}

	err = os.WriteFile(file, d, 0644)
	if err != nil {
		logrus.Errorf("WriteFile: %v", err)
		return err
	}
	return nil // No error
}
