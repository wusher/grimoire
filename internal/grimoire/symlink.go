package grimoire

import (
	"errors"
	"fmt"
	"os"
)

type symlinkSnapshot struct {
	target    string
	directory bool
}

type journalSymlinkSnapshot struct {
	Target    string `json:"target"`
	Directory bool   `json:"directory,omitempty"`
}

type symlinkActionKind string

const (
	symlinkCreate  symlinkActionKind = "create"
	symlinkReplace symlinkActionKind = "replace"
	symlinkRemove  symlinkActionKind = "remove"
	symlinkMove    symlinkActionKind = "move"
)

type symlinkAction struct {
	kind      symlinkActionKind
	path      string
	newPath   string
	target    string
	directory bool
	before    symlinkSnapshot
	created   bool
	removed   bool
}

type journalSymlinkAction struct {
	Kind            symlinkActionKind `json:"kind"`
	Path            string            `json:"path"`
	NewPath         string            `json:"new_path,omitempty"`
	Target          string            `json:"target,omitempty"`
	Directory       bool              `json:"directory,omitempty"`
	BeforeTarget    string            `json:"before_target,omitempty"`
	BeforeDirectory bool              `json:"before_directory,omitempty"`
}

type symlinkPlan struct {
	actions  []symlinkAction
	reserved map[string]bool
}

var (
	createSymlink              = platformCreateSymlink
	readSymlink                = os.Readlink
	removeSymlink              = os.Remove
	errSymlinkRollbackConflict = errors.New("symlink rollback conflict")
)

func snapshotSymlink(path string) (symlinkSnapshot, error) {
	target, err := readSymlink(path)
	if err != nil {
		return symlinkSnapshot{}, err
	}
	directory, err := symlinkDirectory(path)
	if err != nil {
		return symlinkSnapshot{}, err
	}
	return symlinkSnapshot{target: target, directory: directory}, nil
}

func (plan *symlinkPlan) Create(path, target string, directory bool) error {
	if err := requireMissing(path); err != nil {
		return err
	}
	return plan.createMissing(path, target, directory)
}

// createMissing is for a path below a directory that this transaction will
// create. Apply still verifies that the path is absent before it creates it.
func (plan *symlinkPlan) createMissing(path, target string, directory bool) error {
	if err := plan.reserve(path); err != nil {
		return err
	}
	plan.actions = append(plan.actions, symlinkAction{
		kind: symlinkCreate, path: path, target: target, directory: directory,
	})
	return nil
}

func (plan *symlinkPlan) Replace(path string, before symlinkSnapshot, target string, directory bool) error {
	if err := requireSymlink(path, before); err != nil {
		return err
	}
	if err := plan.reserve(path); err != nil {
		return err
	}
	plan.actions = append(plan.actions, symlinkAction{
		kind: symlinkReplace, path: path, target: target, directory: directory, before: before,
	})
	return nil
}

func (plan *symlinkPlan) Remove(path string, before symlinkSnapshot) error {
	if err := requireSymlink(path, before); err != nil {
		return err
	}
	if err := plan.reserve(path); err != nil {
		return err
	}
	plan.actions = append(plan.actions, symlinkAction{kind: symlinkRemove, path: path, before: before})
	return nil
}

func (plan *symlinkPlan) Move(path, newPath string, before symlinkSnapshot, target string, directory bool) error {
	if err := requireSymlink(path, before); err != nil {
		return err
	}
	if err := requireMissing(newPath); err != nil {
		return err
	}
	if err := plan.reserve(path, newPath); err != nil {
		return err
	}
	plan.actions = append(plan.actions, symlinkAction{
		kind: symlinkMove, path: path, newPath: newPath, target: target, directory: directory, before: before,
	})
	return nil
}

func (plan *symlinkPlan) Count() int { return len(plan.actions) }

func (plan *symlinkPlan) Verify() error {
	for _, action := range plan.actions {
		made := symlinkSnapshot{target: action.target, directory: action.directory}
		switch action.kind {
		case symlinkCreate, symlinkReplace:
			if err := requireSymlink(action.path, made); err != nil {
				return err
			}
		case symlinkRemove:
			if err := requireMissing(action.path); err != nil {
				return err
			}
		case symlinkMove:
			if err := requireMissing(action.path); err != nil {
				return err
			}
			if err := requireSymlink(action.newPath, made); err != nil {
				return err
			}
		}
	}
	return nil
}

func (plan *symlinkPlan) Apply() error {
	for index := range plan.actions {
		if err := plan.applyAction(&plan.actions[index]); err != nil {
			return errors.Join(err, plan.Rollback())
		}
	}
	return nil
}

func (plan *symlinkPlan) applyAction(action *symlinkAction) error {
	switch action.kind {
	case symlinkCreate:
		if err := requireMissing(action.path); err != nil {
			return err
		}
		if err := createSymlink(action.target, action.path, action.directory); err != nil {
			return fmt.Errorf("create link %s: %w", action.path, err)
		}
		action.created = true
	case symlinkReplace:
		if err := removeExpectedSymlink(action.path, action.before); err != nil {
			return fmt.Errorf("remove link %s: %w", action.path, err)
		}
		action.removed = true
		if err := createSymlink(action.target, action.path, action.directory); err != nil {
			return fmt.Errorf("create replacement link %s: %w", action.path, err)
		}
		action.created = true
	case symlinkRemove:
		if err := removeExpectedSymlink(action.path, action.before); err != nil {
			return fmt.Errorf("remove link %s: %w", action.path, err)
		}
		action.removed = true
	case symlinkMove:
		if err := requireSymlink(action.path, action.before); err != nil {
			return err
		}
		if err := requireMissing(action.newPath); err != nil {
			return err
		}
		if err := createSymlink(action.target, action.newPath, action.directory); err != nil {
			return fmt.Errorf("create moved link %s: %w", action.newPath, err)
		}
		action.created = true
		if err := removeExpectedSymlink(action.path, action.before); err != nil {
			return fmt.Errorf("remove moved link %s: %w", action.path, err)
		}
		action.removed = true
	default:
		return fmt.Errorf("unknown symlink action %q", action.kind)
	}
	return nil
}

func (plan *symlinkPlan) Rollback() error {
	var failures []error
	for index := len(plan.actions) - 1; index >= 0; index-- {
		if err := rollbackSymlinkAction(&plan.actions[index]); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func rollbackSymlinkAction(action *symlinkAction) error {
	made := symlinkSnapshot{target: action.target, directory: action.directory}
	switch action.kind {
	case symlinkCreate:
		if action.created {
			if err := removeMadeSymlink(action.path, made); err != nil {
				return err
			}
			action.created = false
		}
	case symlinkReplace:
		if action.created {
			if err := removeMadeSymlink(action.path, made); err != nil {
				return err
			}
			action.created = false
		}
		if action.removed {
			if err := restoreMissingSymlink(action.path, action.before); err != nil {
				return err
			}
			action.removed = false
		}
	case symlinkRemove:
		if action.removed {
			if err := restoreMissingSymlink(action.path, action.before); err != nil {
				return err
			}
			action.removed = false
		}
	case symlinkMove:
		var failures []error
		if action.created {
			if err := removeMadeSymlink(action.newPath, made); err != nil {
				failures = append(failures, err)
			} else {
				action.created = false
			}
		}
		if action.removed {
			if err := restoreMissingSymlink(action.path, action.before); err != nil {
				failures = append(failures, err)
			} else {
				action.removed = false
			}
		}
		return errors.Join(failures...)
	}
	return nil
}

func (plan *symlinkPlan) journalActions() []journalSymlinkAction {
	actions := make([]journalSymlinkAction, len(plan.actions))
	for index, action := range plan.actions {
		actions[index] = journalSymlinkAction{
			Kind: action.kind, Path: action.path, NewPath: action.newPath,
			Target: action.target, Directory: action.directory,
			BeforeTarget: action.before.target, BeforeDirectory: action.before.directory,
		}
	}
	return actions
}

func recoverSymlinkPlan(records []journalSymlinkAction) (*symlinkPlan, error) {
	plan := &symlinkPlan{}
	for _, record := range records {
		before := symlinkSnapshot{target: record.BeforeTarget, directory: record.BeforeDirectory}
		wanted := symlinkSnapshot{target: record.Target, directory: record.Directory}
		current, exists, err := inspectSymlink(record.Path)
		if err != nil {
			return nil, err
		}
		switch record.Kind {
		case symlinkCreate:
			if exists && current == wanted {
				continue
			}
			if exists {
				return nil, fmt.Errorf("link %s does not match interrupted mutation", record.Path)
			}
			err = plan.Create(record.Path, record.Target, record.Directory)
		case symlinkReplace:
			if exists && current == wanted {
				continue
			}
			if !exists {
				err = plan.Create(record.Path, record.Target, record.Directory)
			} else if current == before {
				err = plan.Replace(record.Path, before, record.Target, record.Directory)
			} else {
				return nil, fmt.Errorf("link %s does not match interrupted mutation", record.Path)
			}
		case symlinkRemove:
			if !exists {
				continue
			}
			if current != before {
				return nil, fmt.Errorf("link %s does not match interrupted mutation", record.Path)
			}
			err = plan.Remove(record.Path, before)
		case symlinkMove:
			newCurrent, newExists, inspectErr := inspectSymlink(record.NewPath)
			if inspectErr != nil {
				return nil, inspectErr
			}
			if newExists && newCurrent != wanted {
				return nil, fmt.Errorf("link %s does not match interrupted mutation", record.NewPath)
			}
			if exists && current != before {
				return nil, fmt.Errorf("link %s does not match interrupted mutation", record.Path)
			}
			switch {
			case exists && newExists:
				err = plan.Remove(record.Path, before)
			case exists:
				err = plan.Move(record.Path, record.NewPath, before, record.Target, record.Directory)
			case !newExists:
				err = plan.Create(record.NewPath, record.Target, record.Directory)
			}
		default:
			return nil, fmt.Errorf("unknown journal symlink action %q", record.Kind)
		}
		if err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func verifyJournalSymlinkActions(records []journalSymlinkAction) error {
	for _, record := range records {
		wanted := symlinkSnapshot{target: record.Target, directory: record.Directory}
		switch record.Kind {
		case symlinkCreate, symlinkReplace:
			if err := requireSymlink(record.Path, wanted); err != nil {
				return err
			}
		case symlinkRemove:
			if err := requireMissing(record.Path); err != nil {
				return err
			}
		case symlinkMove:
			if err := requireMissing(record.Path); err != nil {
				return err
			}
			if err := requireSymlink(record.NewPath, wanted); err != nil {
				return err
			}
		}
	}
	return nil
}

func (plan *symlinkPlan) reserve(paths ...string) error {
	if plan.reserved == nil {
		plan.reserved = map[string]bool{}
	}
	for _, path := range paths {
		if plan.reserved[path] {
			return fmt.Errorf("link %s is planned more than once", path)
		}
	}
	for _, path := range paths {
		plan.reserved[path] = true
	}
	return nil
}

func inspectSymlink(path string) (symlinkSnapshot, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return symlinkSnapshot{}, false, nil
	}
	if err != nil {
		return symlinkSnapshot{}, false, fmt.Errorf("inspect link %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return symlinkSnapshot{}, true, fmt.Errorf("path %s is not a symlink", path)
	}
	snapshot, err := snapshotSymlink(path)
	if err != nil {
		return symlinkSnapshot{}, true, fmt.Errorf("inspect link %s: %w", path, err)
	}
	return snapshot, true, nil
}

func requireMissing(path string) error {
	_, exists, err := inspectSymlink(path)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("link destination %s is occupied", path)
	}
	return nil
}

func requireSymlink(path string, wanted symlinkSnapshot) error {
	current, exists, err := inspectSymlink(path)
	if err != nil {
		return err
	}
	if !exists || current != wanted {
		return fmt.Errorf("link %s changed after validation", path)
	}
	return nil
}

func removeExpectedSymlink(path string, expected symlinkSnapshot) error {
	if err := requireSymlink(path, expected); err != nil {
		return err
	}
	return removeSymlink(path)
}

func removeMadeSymlink(path string, made symlinkSnapshot) error {
	current, exists, err := inspectSymlink(path)
	if err != nil {
		if exists {
			return fmt.Errorf("%w at %s: current path is not the transaction link", errSymlinkRollbackConflict, path)
		}
		return err
	}
	if !exists {
		return fmt.Errorf("%w at %s: transaction link is missing", errSymlinkRollbackConflict, path)
	}
	if current != made {
		return fmt.Errorf("%w: leave changed link %s in place", errSymlinkRollbackConflict, path)
	}
	if err := removeSymlink(path); err != nil {
		return fmt.Errorf("remove transaction link %s: %w", path, err)
	}
	return nil
}

func restoreMissingSymlink(path string, snapshot symlinkSnapshot) error {
	current, exists, err := inspectSymlink(path)
	if err != nil {
		if exists {
			return fmt.Errorf("%w at %s: destination is not the transaction state", errSymlinkRollbackConflict, path)
		}
		return err
	}
	if exists {
		if current == snapshot {
			return nil
		}
		return fmt.Errorf("%w at %s: destination is occupied", errSymlinkRollbackConflict, path)
	}
	if err := createSymlink(snapshot.target, path, snapshot.directory); err != nil {
		return fmt.Errorf("restore link %s: %w", path, err)
	}
	return nil
}
