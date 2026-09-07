package grimoire

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func Pack(paths Paths, skill Skill) (string, error) {
	output := paths.Output
	if output == "" && paths.Repo == "" && skill.Repository != "" {
		output = filepath.Join(skill.Repository, "output")
	}
	if output == "" {
		var err error
		output, err = paths.OutputDir()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return "", err
	}
	destination := filepath.Join(output, skill.Name+".zip")
	temporary, err := os.CreateTemp(output, "."+skill.Name+"-*.zip")
	if err != nil {
		return "", err
	}
	tempName := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(tempName)
	}

	archive := zip.NewWriter(temporary)
	err = filepath.WalkDir(skill.Dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(filepath.Dir(skill.Dir), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		header.Method = zip.Deflate
		if entry.IsDir() {
			header.Name += "/"
			_, err = archive.CreateHeader(header)
			return err
		}
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(writer, strings.NewReader(target))
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		cleanup()
		return "", fmt.Errorf("pack %s: %w", skill.Name, err)
	}
	if err := archive.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Rename(tempName, destination); err != nil {
		cleanup()
		return "", err
	}
	return destination, nil
}
