package grimoire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Change struct {
	Action  string
	Name    string
	Message string
	Home    string
}

func Hone(paths Paths, dryRun bool) ([]Change, error) {
	unlock, err := lockBindings(paths)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return nil, err
	}
	catalog, err := LoadCatalog(paths)
	if err != nil {
		return nil, err
	}
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return nil, err
	}
	before := ownership.clone()
	plan := &symlinkPlan{}
	var adopted []ownedLink
	var changes []Change

	for _, skill := range catalog.Skills {
		for _, home := range paths.KnownSkillsHomes() {
			link := filepath.Join(home, skill.Name)
			actual, linkErr := linkTarget(link)
			if os.IsNotExist(linkErr) {
				continue
			}
			if linkErr != nil {
				return changes, fmt.Errorf("inspect installed link %s: %w", link, linkErr)
			}
			recorded, tracked := ownership.target(link)
			if !samePath(actual, skill.Dir) || tracked && samePath(actual, recorded) {
				continue
			}
			ownership.set(link, actual)
			adopted = append(adopted, ownedLink{Path: link, Target: actual})
			changes = append(changes, Change{Action: "adopted", Name: skill.Name, Message: "recorded existing valid link", Home: home})
		}
	}

	for _, record := range append([]ownedLink(nil), ownership.Links...) {
		if !knownSkillHome(paths, record.Path) {
			continue
		}
		actual, linkErr := linkTarget(record.Path)
		if linkErr != nil && !os.IsNotExist(linkErr) {
			return changes, fmt.Errorf("inspect owned link %s: %w", record.Path, linkErr)
		}
		if os.IsNotExist(linkErr) || !samePath(actual, record.Target) {
			ownership.remove(record.Path)
			changes = append(changes, Change{Action: "released", Name: filepath.Base(record.Path), Message: "link is missing or was replaced", Home: filepath.Dir(record.Path)})
			continue
		}
		wanted, findErr := catalog.Find(filepath.Base(record.Path))
		if findErr != nil {
			changes = append(changes, Change{Action: "clash", Name: filepath.Base(record.Path), Message: "two skills use this name. Rename one", Home: filepath.Dir(record.Path)})
			continue
		}
		if wanted != nil {
			if samePath(actual, wanted.Dir) {
				continue
			}
			snapshot, err := snapshotSymlink(record.Path)
			if err != nil {
				return changes, fmt.Errorf("inspect owned link %s: %w", record.Path, err)
			}
			if err := plan.Replace(record.Path, snapshot, wanted.Dir, true); err != nil {
				return changes, fmt.Errorf("plan owned link repair: %w", err)
			}
			ownership.set(record.Path, wanted.Dir)
			changes = append(changes, Change{Action: "fixed", Name: wanted.Name, Message: "pointed at the bound skill", Home: filepath.Dir(record.Path)})
			continue
		}
		if _, targetErr := os.Stat(actual); targetErr == nil {
			continue
		} else if !os.IsNotExist(targetErr) {
			return changes, fmt.Errorf("inspect owned target %s: %w", actual, targetErr)
		}
		snapshot, err := snapshotSymlink(record.Path)
		if err != nil {
			return changes, fmt.Errorf("inspect owned link %s: %w", record.Path, err)
		}
		if err := plan.Remove(record.Path, snapshot); err != nil {
			return changes, fmt.Errorf("plan stale owned link removal: %w", err)
		}
		ownership.remove(record.Path)
		changes = append(changes, Change{Action: "removed", Name: filepath.Base(record.Path), Message: "owned target is gone", Home: filepath.Dir(record.Path)})
	}
	if dryRun {
		return changes, nil
	}
	if err := plan.Apply(); err != nil {
		return nil, err
	}
	for _, record := range adopted {
		actual, err := linkTarget(record.Path)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("verify adopted link %s: %w", record.Path, err), plan.Rollback())
		}
		if !samePath(actual, record.Target) {
			return nil, errors.Join(fmt.Errorf("adopted link %s changed before ownership could be saved", record.Path), plan.Rollback())
		}
	}
	if err := plan.Verify(); err != nil {
		return nil, errors.Join(fmt.Errorf("verify honed links before saving ownership: %w", err), plan.Rollback())
	}
	if _, err := commitOwnershipUpdate(paths, before, ownership, plan.Rollback); err != nil {
		return nil, err
	}
	return changes, nil
}
