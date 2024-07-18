package conform

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

func ExportChartDirectory(chart *chart.Chart, targetPath string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	tempDir, err := os.MkdirTemp(wd, "chartDir")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	tgzPath, err := chartutil.Save(chart, tempDir)
	if err != nil {
		return fmt.Errorf("failed to save chart archive to %s", tempDir)
	}

	chartOutputPath := filepath.Join(tempDir, chart.Name())
	if err := Gunzip(tgzPath, chartOutputPath); err != nil {
		return fmt.Errorf("failed to unzip %q to %q: %w", tgzPath, chartOutputPath, err)
	}

	if err := os.RemoveAll(targetPath); err != nil {
		return fmt.Errorf("failed to remove targetPath %q: %w", targetPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create targetPath %q: %w", targetPath, err)
	}

	if err = os.Rename(chartOutputPath, targetPath); err != nil {
		return fmt.Errorf("failed to move %q to %q: %w", chartOutputPath, targetPath, err)
	}

	return nil
}

func stripRootPath(path string) string {
	newPath := filepath.ToSlash(path)
	rootPath := strings.Split(newPath, "/")[0]
	newPath = strings.TrimPrefix(newPath, "/")
	newPath = strings.TrimPrefix(newPath, rootPath)
	newPath = strings.TrimPrefix(newPath, "/")

	return filepath.FromSlash(newPath)
}

func Gunzip(path string, outPath string) error {
	if !strings.HasSuffix(path, ".tgz") && !strings.HasPrefix(path, ".gz") {
		return fmt.Errorf("Expecting file of type .gz or .tgz")
	}

	gzipFile, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open %q: %w", path, err)
	}
	defer gzipFile.Close()

	gzipReader, err := gzip.NewReader(gzipFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		h, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		filePath := filepath.Join(outPath, stripRootPath(h.Name))
		parentPath := filepath.Dir(filePath)
		if err := os.MkdirAll(parentPath, 0755); err != nil {
			return fmt.Errorf("failed to mkdir %q: %w", parentPath, err)
		}

		if h.Typeflag == tar.TypeDir {
			if err = os.MkdirAll(filePath, os.FileMode(h.Mode)); err != nil {
				return err
			}
		} else if h.Typeflag == tar.TypeReg {
			f, err := os.Create(filePath)
			if err != nil {
				return err
			}
			defer f.Close()

			if _, err = io.Copy(f, tarReader); err != nil {
				return err
			}

			if err = os.Chmod(filePath, os.FileMode(h.Mode)); err != nil {
				return err
			}
		} else if h.Name != "pax_global_header" {
			return fmt.Errorf("unknown file type for %s", h.Name)
		}

	}

	return nil
}
