package icons

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// DownloadFiles will download all available icons from chart in index.yaml at assets/icons and return the successfully downloaded files.
// If the file is already downloaded, it will skip the download process but still save the PackageIcon to the map so it can be overridden later
func DownloadFiles(entriesPathsAndIconsMap PackageIconMap) PackageIconMap {
	var failedURLs map[string]string = make(map[string]string)
	var downloadedIcons PackageIconMap = make(PackageIconMap)

	for key, value := range entriesPathsAndIconsMap {
		url := value.Icon        // url coming in the icon field
		filename := value.Name   // chart name from the index.yaml
		ext := filepath.Ext(url) // file extension from the URL

		// GET Request for downloading the icon file
		resp, err := http.Get(url)
		if err != nil {
			failedURLs[filename] = url
			logrus.Errorf("Failed to GET Request for url: %s", url)
			continue
		}
		defer resp.Body.Close()

		// Advanced file type checking in case we could not detect the file type from the URL
		if ext == "" {
			ext = detectMIMEType(resp.Body) // Update the file path with the detected extension
			if ext == "" {
				// could not detect the file type
				failedURLs[filename] = url
				logrus.Errorf("Failed to detect file type for: %s", url)
				continue
			}
		}

		// file path to save the downloaded file
		filePath := partnerDownloadPath + "/" + filename + ext
		// Check if the file already exists and if exists, skip to the next file
		if Exists(filePath) {
			downloadedIcons[key] = ParsePackageToPackageIconOverride(value.Name, value.Path, fmt.Sprintf("file://%s", filePath))
			continue
		}

		// Create and save the icon file locally
		err = saveIconFile(filePath, resp.Body)
		if err != nil {
			failedURLs[filename] = url
			logrus.Errorf("Failed to create/write file: %s", filePath)
			continue
		}
		logrus.Infof("Downloaded icon and saved at: %s", filePath)
		downloadedIcons[key] = ParsePackageToPackageIconOverride(value.Name, value.Path, fmt.Sprintf("file://%s", filePath))
	}
	logrus.Info("Icons asset downloads finished")
	return downloadedIcons
}

// Exists checks if the file already exists
func Exists(filePath string) bool {
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false // File do not exist
	} else if err == nil {
		return true // File exists
	}

	logrus.Errorf("Error checking file: %s - error: %v", filePath, err)
	return false // File might not exist
}

func detectMIMEType(body io.ReadCloser) string {
	buffer := make([]byte, 512)
	_, err := body.Read(buffer)
	if err != nil {
		return ""
	}

	fileType := ""
	mimeType := http.DetectContentType(buffer)
	switch mimeType {
	case "image/jpeg":
		fileType = ".jpg"
	case "image/png":
		fileType = ".png"
	case "image/gif":
		fileType = ".gif"
	case "image/svg+xml":
		fileType = ".svg"
	default:
		fileType = ""
		logrus.Errorf("Unknown file type: %s", mimeType)

	}
	return fileType
}

func saveIconFile(filePath string, body io.ReadCloser) error {
	// Create the file
	out, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("Failed to create file: %w", err)
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, body)
	if err != nil {
		return fmt.Errorf("Failed to write to file: %w", err)
	}
	return nil
}
