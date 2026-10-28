package services

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func zipFiles(zipPath string, baseDir string, fileNames []string) error {
	if len(fileNames) == 0 {
		return fmt.Errorf("zip 文件列表为空")
	}
	out, err := os.OpenFile(zipPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	for _, name := range fileNames {
		src := filepath.Join(baseDir, name)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := filepath.Walk(src, func(path string, info fs.FileInfo, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if info == nil || info.IsDir() {
					return nil
				}
				relName, err := filepath.Rel(baseDir, path)
				if err != nil {
					return err
				}
				return writeZipEntry(zw, path, relName, info)
			}); err != nil {
				return err
			}
			continue
		}
		if err := writeZipEntry(zw, src, name, info); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(zw *zip.Writer, srcPath string, entryName string, info fs.FileInfo) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(strings.TrimPrefix(entryName, string(filepath.Separator)))
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func unzipFile(zipPath string, destDir string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := ensureDir(destDir); err != nil {
		return nil, err
	}
	topLevel := []string{}
	seen := map[string]struct{}{}
	for _, file := range r.File {
		name := strings.TrimSpace(file.Name)
		if name == "" {
			continue
		}
		cleanName := filepath.Clean(name)
		if cleanName == "." || strings.HasPrefix(cleanName, "..") {
			return nil, fmt.Errorf("zip 条目非法: %s", name)
		}
		parts := strings.Split(filepath.ToSlash(cleanName), "/")
		if len(parts) > 0 && parts[0] != "" {
			if _, ok := seen[parts[0]]; !ok {
				seen[parts[0]] = struct{}{}
				topLevel = append(topLevel, parts[0])
			}
		}
		targetPath := filepath.Join(destDir, cleanName)
		if file.FileInfo().IsDir() {
			if err := ensureDir(targetPath); err != nil {
				return nil, err
			}
			continue
		}
		if err := ensureDir(filepath.Dir(targetPath)); err != nil {
			return nil, err
		}
		in, err := file.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			in.Close()
			return nil, err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			in.Close()
			return nil, err
		}
		out.Close()
		in.Close()
	}
	return topLevel, nil
}
