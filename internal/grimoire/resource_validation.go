package grimoire

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type resourceKindSet map[ResourceKind]bool

// validateHookRelative enforces the persisted and selector-level hook scope:
// hooks must be children of the repository's top-level hooks directory.
func validateHookRelative(rel string) (string, error) {
	portable := strings.ReplaceAll(rel, "\\", "/")
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(portable)))
	if !safeRelative(clean) {
		return "", fmt.Errorf("unsafe hook path %q", rel)
	}
	parts := strings.Split(clean, "/")
	if len(parts) < 2 || parts[0] != "hooks" {
		return "", fmt.Errorf("hook path %q must be below the repository's top-level hooks directory", rel)
	}
	return clean, nil
}

// validateHookSelection additionally rejects symlinks in every existing path
// component below the repository root. Missing sources are allowed when
// requireMarker is false so stale bindings can still be repaired or unbound.
func validateHookSelection(repository, rel string, requireMarker bool) error {
	clean, err := validateHookRelative(rel)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(repository) {
		return fmt.Errorf("hook repository path %q must be absolute", repository)
	}
	root, err := absolute(repository)
	if err != nil {
		return err
	}
	resolvedRoot := root
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		resolvedRoot = resolved
	} else if !os.IsNotExist(resolveErr) {
		return fmt.Errorf("resolve hook repository %s: %w", root, resolveErr)
	}

	current := root
	for _, component := range strings.Split(clean, "/") {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if requireMarker {
				return fmt.Errorf("hook path %s is missing", current)
			}
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect hook path %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("hook path %s contains a symlink", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("hook path component %s is not a directory", current)
		}
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr != nil {
			return fmt.Errorf("resolve hook path %s: %w", current, resolveErr)
		}
		if !pathWithin(resolvedRoot, resolved) {
			return fmt.Errorf("hook path %s resolves outside repository %s", current, root)
		}
	}

	marker := filepath.Join(current, HookKind.Marker())
	info, statErr := os.Lstat(marker)
	if os.IsNotExist(statErr) {
		if requireMarker {
			return fmt.Errorf("%s does not contain a regular %s", current, HookKind.Marker())
		}
		return nil
	}
	if statErr != nil {
		return fmt.Errorf("inspect hook marker %s: %w", marker, statErr)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s does not contain a regular non-symlink %s", current, HookKind.Marker())
	}
	return nil
}

func validateHookResource(resource Skill, requireMarker bool) error {
	if resource.Repository == "" {
		return fmt.Errorf("hook %s has no repository root", resource.Dir)
	}
	root, err := absolute(resource.Repository)
	if err != nil {
		return err
	}
	dir, err := absolute(resource.Dir)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("resolve hook %s relative to %s: %w", dir, root, err)
	}
	return validateHookSelection(root, rel, requireMarker)
}

// validateBindingMutationJournal confines persisted recovery actions to the
// installed-resource namespace and to sources named by the journal states.
// It deliberately uses lexical home matching: a physical alias of a familiar
// home is not authority to mutate that alias during crash recovery.
func validateBindingMutationJournal(paths Paths, journal bindingMutationJournal) error {
	allowed, err := journalAllowedResourceTargets(journal.Before, journal.After)
	if err != nil {
		return err
	}
	for index, action := range journal.Links {
		if err := validateJournalSymlinkAction(paths, action, allowed); err != nil {
			return fmt.Errorf("link action %d: %w", index, err)
		}
	}
	legacyPersistedClaims := map[ownedLink]bool{}
	if journal.Ownership != nil {
		if journal.Ownership.Version != ownershipVersion {
			return fmt.Errorf("ownership has unsupported version %d", journal.Ownership.Version)
		}
		beforeClaims := map[ownedLink]bool{}
		if journal.BeforeOwnership != nil {
			if journal.BeforeOwnership.Version != ownershipVersion {
				return fmt.Errorf("before ownership has unsupported version %d", journal.BeforeOwnership.Version)
			}
			seenBefore := map[string]bool{}
			for index, link := range journal.BeforeOwnership.Links {
				if link.Path == "" || !filepath.IsAbs(link.Path) || filepath.Clean(link.Path) != link.Path {
					return fmt.Errorf("before ownership link %d: path %q must be absolute and clean", index, link.Path)
				}
				if link.Target == "" || !filepath.IsAbs(link.Target) || filepath.Clean(link.Target) != link.Target {
					return fmt.Errorf("before ownership link %d: target %q must be absolute and clean", index, link.Target)
				}
				if seenBefore[link.Path] {
					return fmt.Errorf("before ownership link %d: duplicate path %s", index, link.Path)
				}
				seenBefore[link.Path] = true
				beforeClaims[link] = true
			}
		}
		if journal.Version == legacyBindingMutationVersion && journal.BeforeOwnership == nil {
			persisted, legacyErr := configuredOwnership(paths)
			if legacyErr != nil {
				return fmt.Errorf("verify legacy journal ownership: %w", legacyErr)
			}
			for _, link := range persisted.Links {
				legacyPersistedClaims[link] = true
			}
		}
		actionClaims := journalOwnershipActionClaims(journal.Links)
		seen := map[string]bool{}
		retainedClaim := false
		for index, link := range journal.Ownership.Links {
			kind, clean, err := journalLinkKind(paths, link.Path)
			if err != nil {
				return fmt.Errorf("ownership link %d: %w", index, err)
			}
			if seen[clean] {
				return fmt.Errorf("ownership link %d: duplicate path %s", index, clean)
			}
			seen[clean] = true
			if beforeClaims[link] {
				retainedClaim = true
				continue
			}
			if legacyPersistedClaims[link] {
				continue
			}
			if err := requireJournalTarget(link.Target, kind, allowed, "ownership target"); err != nil {
				return fmt.Errorf("ownership link %d: %w", index, err)
			}
			if !actionClaims[link] {
				return fmt.Errorf("ownership link %d: new or changed claim is not justified by an installed-link action", index)
			}
		}
		if retainedClaim {
			if err := validateJournalOwnershipPhase(paths, journal); err != nil {
				return err
			}
		}
		if journal.Version == legacyBindingMutationVersion && journal.BeforeOwnership == nil {
			if _, err := validateLegacyJournalOwnership(paths, journal); err != nil {
				return err
			}
		}
	} else if journal.BeforeOwnership != nil {
		return fmt.Errorf("before ownership cannot be present without ownership")
	}
	if err := validateJournalDestructiveActionOwnership(paths, journal); err != nil {
		return err
	}
	return nil
}

func validateJournalDestructiveActionOwnership(paths Paths, journal bindingMutationJournal) error {
	if journal.Version == legacyBindingMutationVersion && journal.BeforeOwnership == nil && journal.Ownership != nil {
		// validateLegacyJournalOwnership has already proven each destructive
		// action against persisted ownership or a completed after-state.
		return nil
	}
	needsProof := false
	beforeClaims := map[ownedLink]bool{}
	if journal.BeforeOwnership != nil {
		for _, link := range journal.BeforeOwnership.Links {
			beforeClaims[link] = true
		}
	}
	for index, action := range journal.Links {
		switch action.Kind {
		case symlinkReplace, symlinkRemove, symlinkMove:
			needsProof = true
			claim := ownedLink{Path: action.Path, Target: action.BeforeTarget}
			if !beforeClaims[claim] {
				return fmt.Errorf("link action %d: destructive action lacks prior ownership", index)
			}
		}
	}
	if !needsProof {
		return nil
	}
	if journal.BeforeOwnership == nil || journal.Ownership == nil {
		return fmt.Errorf("destructive link actions require before and after ownership state")
	}
	return validateJournalOwnershipPhase(paths, journal)
}

// validateLegacyJournalOwnership recovers journals written before the
// before_ownership snapshot existed. The persisted ownership file is the only
// admissible pre-state: every difference between it and the journal's desired
// state must be explained by an exact validated link action. If ownership is
// already in the after-state, destructive actions are accepted only when all
// link actions are also complete.
func validateLegacyJournalOwnership(paths Paths, journal bindingMutationJournal) (ownershipState, error) {
	persisted, err := configuredOwnership(paths)
	if err != nil {
		return ownershipState{}, fmt.Errorf("verify legacy journal ownership: %w", err)
	}
	after := *journal.Ownership
	beforeClaims := map[ownedLink]bool{}
	for _, link := range persisted.Links {
		beforeClaims[link] = true
	}
	afterClaims := map[ownedLink]bool{}
	for _, link := range after.Links {
		afterClaims[link] = true
	}
	finalActions := journalOwnershipActionClaims(journal.Links)
	priorActions := map[ownedLink]bool{}
	for _, action := range journal.Links {
		switch action.Kind {
		case symlinkReplace, symlinkRemove, symlinkMove:
			priorActions[ownedLink{Path: action.Path, Target: action.BeforeTarget}] = true
		}
	}
	for claim := range afterClaims {
		if beforeClaims[claim] || finalActions[claim] {
			continue
		}
		return ownershipState{}, fmt.Errorf("legacy journal ownership claim %s is not justified by persisted ownership or an installed-link action", claim.Path)
	}
	for claim := range beforeClaims {
		if afterClaims[claim] || priorActions[claim] {
			continue
		}
		return ownershipState{}, fmt.Errorf("legacy journal drops persisted ownership claim %s without a matching installed-link action", claim.Path)
	}

	afterComplete := false
	for index, action := range journal.Links {
		switch action.Kind {
		case symlinkReplace, symlinkRemove, symlinkMove:
			prior := ownedLink{Path: action.Path, Target: action.BeforeTarget}
			if beforeClaims[prior] {
				continue
			}
			if !ownershipEqual(persisted, after) {
				return ownershipState{}, fmt.Errorf("link action %d: destructive action lacks persisted prior ownership", index)
			}
			if !afterComplete {
				if err := verifyJournalSymlinkActions(journal.Links); err != nil {
					return ownershipState{}, fmt.Errorf("destructive action lacks prior ownership and persisted ownership matches the legacy after-state, but installed-link actions are incomplete: %w", err)
				}
				afterComplete = true
			}
		}
	}
	return persisted, nil
}

// validateJournalOwnershipPhase accepts the persisted before-state, or the
// persisted after-state only when every journaled link action is already in
// its after-state. This distinguishes a legitimate crash after ownership was
// saved from a forged journal that merely claims the mutation already ran.
func validateJournalOwnershipPhase(paths Paths, journal bindingMutationJournal) error {
	persisted, err := configuredOwnership(paths)
	if err != nil {
		return fmt.Errorf("verify journal ownership: %w", err)
	}
	if journal.BeforeOwnership != nil && ownershipEqual(persisted, *journal.BeforeOwnership) {
		return nil
	}
	if journal.Ownership != nil && ownershipEqual(persisted, *journal.Ownership) {
		if err := verifyJournalSymlinkActions(journal.Links); err != nil {
			return fmt.Errorf("persisted ownership matches the after-state but installed-link actions are incomplete: %w", err)
		}
		return nil
	}
	return fmt.Errorf("journal ownership does not match persisted ownership state")
}

func journalOwnershipActionClaims(actions []journalSymlinkAction) map[ownedLink]bool {
	claims := map[ownedLink]bool{}
	for _, action := range actions {
		switch action.Kind {
		case symlinkCreate, symlinkReplace:
			claims[ownedLink{Path: action.Path, Target: action.Target}] = true
		case symlinkMove:
			claims[ownedLink{Path: action.NewPath, Target: action.Target}] = true
		}
	}
	return claims
}

func journalAllowedResourceTargets(states ...[]BoundRepository) (map[string]resourceKindSet, error) {
	allowed := map[string]resourceKindSet{}
	for _, bindings := range states {
		for _, binding := range bindings {
			if !filepath.IsAbs(binding.Path) || filepath.Clean(binding.Path) != binding.Path {
				return nil, fmt.Errorf("repository path %q must be absolute and clean", binding.Path)
			}
			for _, selection := range []struct {
				kind  ResourceKind
				paths []string
			}{{SkillKind, binding.Skills}, {HookKind, binding.Hooks}} {
				for _, rel := range selection.paths {
					clean := filepath.Clean(filepath.FromSlash(rel))
					if selection.kind == HookKind {
						var err error
						clean, err = validateHookRelative(rel)
						if err != nil {
							return nil, err
						}
					} else if !safeRelative(clean) {
						return nil, fmt.Errorf("unsafe skill path %q", rel)
					}
					target := filepath.Clean(filepath.Join(binding.Path, clean))
					if !filepath.IsAbs(target) {
						return nil, fmt.Errorf("resource target %q must be absolute", target)
					}
					if allowed[target] == nil {
						allowed[target] = resourceKindSet{}
					}
					allowed[target][selection.kind] = true
				}
			}
		}
	}
	return allowed, nil
}

func validateJournalSymlinkAction(paths Paths, action journalSymlinkAction, allowed map[string]resourceKindSet) error {
	kind, _, err := journalLinkKind(paths, action.Path)
	if err != nil {
		return err
	}
	requireNoNewPath := func() error {
		if action.NewPath != "" {
			return fmt.Errorf("%s action cannot have new_path", action.Kind)
		}
		return nil
	}
	requireTarget := func(value, label string) error {
		return requireJournalTarget(value, kind, allowed, label)
	}
	requireNoTarget := func(value, label string) error {
		if value != "" {
			return fmt.Errorf("%s action cannot have %s", action.Kind, label)
		}
		return nil
	}

	switch action.Kind {
	case symlinkCreate:
		if err := requireNoNewPath(); err != nil {
			return err
		}
		if err := requireTarget(action.Target, "target"); err != nil {
			return err
		}
		if err := requireNoTarget(action.BeforeTarget, "before_target"); err != nil {
			return err
		}
		if !action.Directory || action.BeforeDirectory {
			return fmt.Errorf("create action must describe only a directory target")
		}
	case symlinkReplace:
		if err := requireNoNewPath(); err != nil {
			return err
		}
		if err := requireTarget(action.Target, "target"); err != nil {
			return err
		}
		if err := requireTarget(action.BeforeTarget, "before_target"); err != nil {
			return err
		}
		if !action.Directory || !action.BeforeDirectory {
			return fmt.Errorf("replace action targets must be directories")
		}
	case symlinkRemove:
		if err := requireNoNewPath(); err != nil {
			return err
		}
		if err := requireNoTarget(action.Target, "target"); err != nil {
			return err
		}
		if err := requireTarget(action.BeforeTarget, "before_target"); err != nil {
			return err
		}
		if action.Directory || !action.BeforeDirectory {
			return fmt.Errorf("remove action must describe only a previous directory target")
		}
	case symlinkMove:
		newKind, _, err := journalLinkKind(paths, action.NewPath)
		if err != nil {
			return fmt.Errorf("new_path: %w", err)
		}
		if newKind != kind {
			return fmt.Errorf("move action changes resource kind from %s to %s", kind.Name(), newKind.Name())
		}
		if err := requireTarget(action.Target, "target"); err != nil {
			return err
		}
		if err := requireTarget(action.BeforeTarget, "before_target"); err != nil {
			return err
		}
		if !action.Directory || !action.BeforeDirectory {
			return fmt.Errorf("move action targets must be directories")
		}
	default:
		return fmt.Errorf("unknown symlink action %q", action.Kind)
	}
	return nil
}

func journalLinkKind(paths Paths, path string) (ResourceKind, string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return SkillKind, "", fmt.Errorf("link path %q must be absolute and clean", path)
	}
	parent := filepath.Dir(path)
	kinds := resourceKindSet{}
	for _, kind := range []ResourceKind{SkillKind, HookKind} {
		for _, familiar := range familiarMetadata {
			root, err := absolute(familiar.home(paths))
			if err != nil {
				continue
			}
			home := filepath.Join(root, kind.Plural())
			if home == parent {
				if err := validateJournalResourceHome(root, home); err != nil {
					return SkillKind, "", err
				}
				kinds[kind] = true
			}
		}
	}
	if len(kinds) == 0 {
		return SkillKind, "", fmt.Errorf("link path %s is not a direct child of a known familiar resource home", path)
	}
	if len(kinds) != 1 {
		return SkillKind, "", fmt.Errorf("link path %s has an ambiguous familiar resource home", path)
	}
	for kind := range kinds {
		return kind, path, nil
	}
	panic("unreachable")
}

// validateJournalResourceHome prevents crash recovery from traversing a
// symlinked familiar root or resource home. Higher system ancestors are not
// inspected because platform temp and home paths may legitimately contain
// aliases (for example /var on macOS). Installer transactions create both
// directories before journaling link actions, so recovery fails closed if
// either path is absent or is not a real directory.
func validateJournalResourceHome(root, home string) error {
	for _, current := range []string{root, home} {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return fmt.Errorf("familiar resource home %s is missing", home)
		}
		if err != nil {
			return fmt.Errorf("inspect familiar resource home %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("familiar resource home %s has symlinked path component %s", home, current)
		}
		if !info.IsDir() {
			return fmt.Errorf("familiar resource home %s has non-directory path component %s", home, current)
		}
	}
	return nil
}

func requireJournalTarget(target string, kind ResourceKind, allowed map[string]resourceKindSet, label string) error {
	if target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return fmt.Errorf("%s %q must be absolute and clean", label, target)
	}
	if !allowed[target][kind] {
		return fmt.Errorf("%s %s is not a selected %s source", label, target, kind.Name())
	}
	return nil
}

// resourceMarker reports marker presence without swallowing permission,
// inaccessible-path, or invalid-type errors. Skills retain their released
// symlink-following behavior; hook markers must be regular non-symlinks.
func resourceMarker(path string, kind ResourceKind) (bool, error) {
	var (
		info os.FileInfo
		err  error
	)
	if kind == HookKind {
		info, err = os.Lstat(path)
	} else {
		info, err = os.Stat(path)
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if kind == HookKind {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, fmt.Errorf("%s must be a regular non-symlink file", path)
		}
		return true, nil
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory, not a skill marker", path)
	}
	return true, nil
}
