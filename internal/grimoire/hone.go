package grimoire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Change struct {
	Action  string
	Name    string
	Message string
	Home    string
}

func Hone(paths Paths, dryRun bool) ([]Change, error) {
	catalog, err := LoadCatalog(paths)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, home := range paths.SkillsHomes() {
		entries, err := os.ReadDir(home)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", home, err)
		}
		for _, entry := range entries {
			link := filepath.Join(home, entry.Name())
			raw, err := os.Readlink(link)
			if err != nil {
				continue
			}
			target := resolveLink(link, raw)
			wanted, findErr := catalog.Find(entry.Name())
			if findErr != nil {
				changes = append(changes, Change{Action: "clash", Name: entry.Name(), Message: "two skills use this name. Rename one", Home: home})
				continue
			}
			if wanted != nil && samePath(target, wanted.Dir) {
				continue
			}
			_, targetErr := os.Stat(target)
			inside := catalog.Owns(target)
			if targetErr == nil && !inside {
				continue // a live link owned by someone else
			}
			if wanted != nil {
				if !dryRun {
					if err := replaceSymlink(link, wanted.Dir); err != nil {
						return nil, err
					}
				}
				changes = append(changes, Change{Action: "fixed", Name: entry.Name(), Message: "pointed at the bound skill", Home: home})
				continue
			}
			if targetErr != nil || inside {
				if !dryRun {
					if err := os.Remove(link); err != nil {
						return nil, err
					}
				}
				message := "target folder is gone"
				if targetErr != nil {
					message = "broken link"
				}
				changes = append(changes, Change{Action: "removed", Name: entry.Name(), Message: message, Home: home})
			}
		}
	}
	return changes, nil
}

func pathInside(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func replaceSymlink(link, target string) error {
	if err := os.Remove(link); err != nil {
		return err
	}
	return os.Symlink(target, link)
}
