package icons

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	p "github.com/rancher/partner-charts-ci/pkg/paths"
	"github.com/rancher/partner-charts-ci/pkg/utils"
)

// possible extensions for the icons
var validExtensions []string = []string{".png", ".jpg", ".jpeg", ".svg", ".ico"}

// GetDownloadedIconPath checks if the package with name packageName has
// an icon downloaded. If so, it returns the path. Otherwise it returns
// an error.
func GetDownloadedIconPath(paths p.Paths, packageName string) (string, error) {
	for _, ext := range validExtensions {
		filePath := filepath.Join(paths.Icons, packageName+ext)
		if exists, err := utils.Exists(filePath); err != nil {
			return "", fmt.Errorf("failed to check %s for existence: %w", filePath, err)
		} else if exists {
			return filePath, nil
		}
	}

	return "", fmt.Errorf("no icon found for package %q", packageName)
}

// DownloadIcon downloads the icon at iconUrl to the icon file path
// for package packageName. Returns the path to the icon.
func DownloadIcon(paths p.Paths, iconURL, packageName string) (localIconPath string, err error) {
	resp, err := http.Get(iconURL)
	if err != nil {
		return "", fmt.Errorf("failed to http get %q: %w", iconURL, err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("got non-2xx status code on response: %s", resp.Status)
	}

	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	ext := filepath.Ext(iconURL)
	if ext == "" {
		ext, err = getExtension(contents)
		if err != nil {
			return "", fmt.Errorf("failed to get file extension: %w", err)
		}
	}

	localIconPath = filepath.Join(paths.Icons, packageName+ext)
	if err := os.WriteFile(localIconPath, contents, 0o644); err != nil {
		return "", fmt.Errorf("failed to write response to file: %w", err)
	}

	return localIconPath, err
}

func getExtension(data []byte) (string, error) {
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg":
		return ".jpg", nil
	case "image/png":
		return ".png", nil
	case "image/gif":
		return ".gif", nil
	case "image/svg+xml":
		return ".svg", nil
	}
	return "", fmt.Errorf("unknown file type %q", mimeType)
}
