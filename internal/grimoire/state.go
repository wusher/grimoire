package grimoire

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writeJSONFile(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	cleanup := func() error {
		return errors.Join(temporary.Close(), removeIfExists(name))
	}
	if _, err := temporary.Write(body); err != nil {
		return errors.Join(err, cleanup())
	}
	if err := temporary.Sync(); err != nil {
		return errors.Join(err, cleanup())
	}
	if err := temporary.Close(); err != nil {
		return errors.Join(err, removeIfExists(name))
	}
	if err := replaceFile(name, path); err != nil {
		return errors.Join(err, removeIfExists(name))
	}
	return syncParentDirectory(dir)
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("remove %s: %w", path, err)
}
