package grimoire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type InstallStatus string

const (
	Installed      InstallStatus = "installed"
	Removed        InstallStatus = "removed"
	Missing        InstallStatus = "missing"
	InstallBlocked InstallStatus = "blocked"
)

type InstallResult struct {
	Status  InstallStatus
	Skill   Skill
	Message string
	Changes []PathChange
}

func Install(paths Paths, skill Skill) InstallResult {
	links := skill.LinkPaths()
	if len(links) == 0 {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: "no familiar chosen"}
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	before := ownership.clone()
	plan := &symlinkPlan{}
	adopted := 0
	for _, link := range links {
		info, err := os.Lstat(link)
		if os.IsNotExist(err) {
			if err := plan.Create(link, skill.Dir, true); err != nil {
				return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot plan install: %v", err)}
			}
			continue
		}
		if err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot inspect destination: %v", err)}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: "a real folder already holds this name"}
		}
		actual, err := linkTarget(link)
		if err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot inspect destination: %v", err)}
		}
		if !samePath(actual, skill.Dir) {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: "a link that is not ours already holds this name"}
		}
		recorded, tracked := ownership.target(link)
		if !tracked || !samePath(actual, recorded) {
			ownership.set(link, actual)
			adopted++
		}
	}
	for _, link := range links {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
		}
	}
	if err := plan.Apply(); err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("create installed links: %v", err)}
	}
	for _, link := range links {
		actual, err := linkTarget(link)
		if err != nil {
			return InstallResult{
				Status: InstallBlocked, Skill: skill,
				Message: errors.Join(fmt.Errorf("verify installed link %s: %w", link, err), plan.Rollback()).Error(),
			}
		}
		if !samePath(actual, skill.Dir) {
			return InstallResult{
				Status: InstallBlocked, Skill: skill,
				Message: errors.Join(fmt.Errorf("installed link %s changed before ownership could be saved", link), plan.Rollback()).Error(),
			}
		}
		ownership.set(link, actual)
	}
	if plan.Count() > 0 || adopted > 0 {
		if _, err := commitOwnershipUpdate(paths, before, ownership, plan.Rollback); err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
		}
	}
	if plan.Count() == 0 {
		message := "already installed"
		changes := []PathChange{}
		if adopted > 0 {
			message += "; ownership recorded"
			for _, link := range links {
				changes = append(changes, PathChange{Action: "recorded ownership", Path: link, Target: skill.Dir})
			}
		}
		return InstallResult{Status: AlreadyStatus, Skill: skill, Message: message, Changes: changes}
	}
	changes := make([]PathChange, 0, len(links))
	for _, link := range links {
		changes = append(changes, PathChange{Action: "created link", Path: link, Target: skill.Dir})
	}
	return InstallResult{Status: Installed, Skill: skill, Message: "installed", Changes: changes}
}

const AlreadyStatus InstallStatus = "already"

func Uninstall(paths Paths, skill Skill) InstallResult {
	links := skill.LinkPaths()
	if len(links) == 0 {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: "no familiar chosen"}
	}
	unlock, err := lockBindings(paths)
	if err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	defer unlock()
	if err := recoverBindingMutation(paths); err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	ownership, err := configuredOwnership(paths)
	if err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
	}
	before := ownership.clone()
	plan := &symlinkPlan{}
	released := 0
	for _, link := range links {
		recorded, tracked := ownership.target(link)
		actual, readErr := linkTarget(link)
		if os.IsNotExist(readErr) {
			if tracked {
				ownership.remove(link)
				released++
			}
			continue
		}
		if readErr != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot inspect installed link: %v", readErr)}
		}
		if !tracked {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: "ownership is not recorded for this link; run grimoire hone first"}
		}
		if !samePath(actual, recorded) {
			ownership.remove(link)
			released++
			continue
		}
		snapshot, err := snapshotSymlink(link)
		if err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot inspect installed link: %v", err)}
		}
		if err := plan.Remove(link, snapshot); err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("cannot plan uninstall: %v", err)}
		}
		ownership.remove(link)
	}
	if err := plan.Apply(); err != nil {
		return InstallResult{Status: InstallBlocked, Skill: skill, Message: fmt.Sprintf("remove installed links: %v", err)}
	}
	if plan.Count() > 0 || released > 0 {
		if _, err := commitOwnershipUpdate(paths, before, ownership, plan.Rollback); err != nil {
			return InstallResult{Status: InstallBlocked, Skill: skill, Message: err.Error()}
		}
	}
	if plan.Count() == 0 {
		message := "not installed"
		changes := []PathChange{}
		if released > 0 {
			message += "; stale ownership removed"
			for _, link := range links {
				changes = append(changes, PathChange{Action: "released ownership", Path: link})
			}
		}
		return InstallResult{Status: Missing, Skill: skill, Message: message, Changes: changes}
	}
	changes := make([]PathChange, 0, len(links))
	for _, link := range links {
		changes = append(changes, PathChange{Action: "removed link", Path: link, Target: skill.Dir})
	}
	return InstallResult{Status: Removed, Skill: skill, Message: "removed", Changes: changes}
}
