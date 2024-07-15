package icons

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// possible extensions for the icons
var validExtensions []string = []string{".png", ".jpg", ".jpeg", ".svg", ".ico"}

// GetDownloadedIconPath checks if the package with name packageName has
// an icon downloaded. If so, it returns the path. Otherwise it returns
// an error.
func GetDownloadedIconPath(packageName string) (string, error) {
	for _, ext := range validExtensions {
		filePath := fmt.Sprintf("assets/icons/%s%s", packageName, ext)
		if exist := Exists(filePath); exist {
			return filePath, nil
		}
	}

	return "", fmt.Errorf("no icon found for package %q", packageName)
}

// EnsureIconDownloaded downloads the icon at iconUrl to the icon file path
// for package packageName. If a file already exists at this path, the
// download is skipped. Returns the path to the icon.
func EnsureIconDownloaded(iconUrl, packageName string) (string, error) {
	if localIconPath, err := GetDownloadedIconPath(packageName); err == nil {
		return localIconPath, nil
	}

	resp, err := http.Get(iconUrl)
	if err != nil {
		return "", fmt.Errorf("failed to http get %q: %w", iconUrl, err)
	}
	defer resp.Body.Close()

	ext := filepath.Ext(iconUrl)
	if ext == "" {
		ext = getExtension(resp.Body)
		if ext == "" {
			return "", fmt.Errorf("failed to get file extension: %w", err)
		}
	}

	localIconPath := filepath.Join("assets", "icons", packageName+ext)
	destFile, err := os.Create(localIconPath)
	if err != nil {
		return "", fmt.Errorf("failed to create local icon file %q: %w", localIconPath, err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to copy response to dest file: %w", err)
	}

	return localIconPath, nil
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

func getExtension(body io.ReadCloser) string {
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
