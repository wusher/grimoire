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

const (
	packTemporaryPrefix = ".grimoire-pack-"
	packTemporarySuffix = ".tmp"
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
	outputPath, err := resolvedPath(output)
	if err != nil {
		return "", err
	}
	skillPath, err := resolvedPath(skill.Dir)
	if err != nil {
		return "", err
	}

	destination := filepath.Join(output, skill.Name+".zip")
	destinationPath := filepath.Join(outputPath, skill.Name+".zip")
	temporaryDir := outputPath
	if pathWithin(skillPath, outputPath) {
		temporaryDir = filepath.Dir(skillPath)
	}
	temporary, err := os.CreateTemp(temporaryDir, packTemporaryPrefix+"*"+packTemporarySuffix)
	if err != nil && temporaryDir != outputPath {
		temporary, err = os.CreateTemp(outputPath, packTemporaryPrefix+"*"+packTemporarySuffix)
	}
	if err != nil {
		return "", err
	}
	tempPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(tempPath)
	}

	archive := zip.NewWriter(temporary)
	err = filepath.WalkDir(skillPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == tempPath || path == destinationPath || isPackTemporary(entry.Name()) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		relativePath, err := filepath.Rel(skillPath, path)
		if err != nil {
			return err
		}
		rel := filepath.Base(filepath.Clean(skill.Dir))
		if relativePath != "." {
			rel = filepath.Join(rel, relativePath)
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
	if err := renamePackedArchive(tempPath, destinationPath, outputPath); err != nil {
		cleanup()
		return "", err
	}
	cleanup()
	return destination, nil
}

func renamePackedArchive(temporaryPath, destinationPath, outputPath string) error {
	renameErr := os.Rename(temporaryPath, destinationPath)
	if renameErr == nil || filepath.Clean(filepath.Dir(temporaryPath)) == filepath.Clean(outputPath) {
		return renameErr
	}

	source, err := os.Open(temporaryPath)
	if err != nil {
		return err
	}
	replacement, err := os.CreateTemp(outputPath, packTemporaryPrefix+"*"+packTemporarySuffix)
	if err != nil {
		_ = source.Close()
		return err
	}
	replacementPath := replacement.Name()
	defer func() {
		_ = source.Close()
		_ = replacement.Close()
		_ = os.Remove(replacementPath)
	}()

	_, copyErr := io.Copy(replacement, source)
	sourceCloseErr := source.Close()
	replacementCloseErr := replacement.Close()
	if copyErr != nil {
		return copyErr
	}
	if sourceCloseErr != nil {
		return sourceCloseErr
	}
	if replacementCloseErr != nil {
		return replacementCloseErr
	}
	return os.Rename(replacementPath, destinationPath)
}

func resolvedPath(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolutePath)
}

func pathWithin(root, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	return err == nil && relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator))
}

func isPackTemporary(name string) bool {
	return strings.HasPrefix(name, packTemporaryPrefix) && strings.HasSuffix(name, packTemporarySuffix)
}
