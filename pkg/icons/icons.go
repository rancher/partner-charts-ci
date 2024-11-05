package icons

import (
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
func GetDownloadedIconPath(packageName string) (string, error) {
	for _, ext := range validExtensions {
		filePath := fmt.Sprintf("assets/icons/%s%s", packageName, ext)
		if exists, err := utils.Exists(filePath); err != nil {
			return "", fmt.Errorf("failed to check %s for existence: %w", filePath, err)
		} else if exists {
			return filePath, nil
		}
	}

	return "", fmt.Errorf("no icon found for package %q", packageName)
}

// EnsureIconDownloaded downloads the icon at iconUrl to the icon file path
// for package packageName. If a file already exists at this path, the
// download is skipped. Returns the path to the icon.
func EnsureIconDownloaded(paths p.Paths, iconUrl, packageName string) (string, error) {
	if localIconPath, err := GetDownloadedIconPath(packageName); err == nil {
		return localIconPath, nil
	}

	resp, err := http.Get(iconUrl)
	if err != nil {
		return "", fmt.Errorf("failed to http get %q: %w", iconUrl, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("got non-2xx status code on response: %s", resp.Status)
	}

	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	ext := filepath.Ext(iconUrl)
	if ext == "" {
		ext, err = getExtension(contents)
		if err != nil {
			return "", fmt.Errorf("failed to get file extension: %w", err)
		}
	}

	localIconPath := filepath.Join(paths.Icons, packageName+ext)
	if err := os.WriteFile(localIconPath, contents, 0o644); err != nil {
		return "", fmt.Errorf("failed to write response to file: %w", err)
	}

	return localIconPath, nil
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
