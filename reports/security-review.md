# Security review: bind and cast parity

## Scope

Reviewed repository skill discovery, explicit binding, and installation while
implementing the compound CLI `bind` operation.

## Finding: symlinked skill manifests

**Severity:** Medium

`DiscoverRepositorySkills`, `selectedPaths`, and `validateInstallSource`
previously used checks that followed `SKILL.md` symlinks. A repository could
therefore present an external regular file as its skill manifest. This crossed
the expected repository skill boundary and allowed stale or crafted `Skill`
values to reach installation.

## Resolution

- Discovery now ignores `SKILL.md` entries that are not regular files.
- Explicit binding rejects a skill unless its `SKILL.md` is a regular,
  non-symlink file.
- Installation performs the same non-following validation before any link or
  ownership mutation.
- A regression test creates a `SKILL.md` symlink to an external file and proves
  that it is neither discovered, bound, nor installed.

The source directory itself may still be reached through a repository path as
allowed by the existing contract; this review only establishes the manifest
boundary needed by the implemented bind/cast flow.

## Residual risks

- **Low–medium: source path TOCTOU.** Repository source directories and parent
  components can still change concurrently between validation and link
  creation. Fully eliminating this would require descriptor-relative traversal
  and platform-specific handling.
- **Low: separate bind and install transactions.** The catalog update commits
  before installation. A crash, blocked destination, or concurrent operation
  can therefore leave a valid binding only partially installed. The CLI reports
  this state and supports an idempotent `grimoire bind` retry.
- **Low: trusted familiar-home parents.** Destination parent components are
  assumed not to be controlled by a hostile actor. Existing destination files,
  directories, and foreign symlinks are still preserved rather than replaced.

No shell-injection path was found: Git commands use argument vectors and skill
selectors are not evaluated by a shell.
