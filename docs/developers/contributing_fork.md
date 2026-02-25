# Contributing to ShowHeroes Prebid Server Fork

This document describes how to work with the ShowHeroes fork of Prebid Server.

## Repository Structure

- **Upstream (Original):** `git@github.com:prebid/prebid-server.git`
- **Fork (ShowHeroes):** `git@github.com:showheroes/prebid-server.git`

### Branch Strategy

| Branch | Purpose |
|--------|---------|
| `master` | Tracks upstream changes from `prebid/prebid-server`. **Do not commit directly.** |
| `release` | Production branch based on a specific upstream tag. All PRs should target this branch. |

**Current upstream tag:** `v3.30.0`

> **Note:** The `release` branch is intentionally **not** in sync with `origin/master`. It is based on a specific tagged release from upstream. This means `origin/master` contains commits that `release` does not have, and this is expected until we decide to upgrade to a newer tag.

## Initial Setup

### 1. Clone the Original Repository

```bash
git clone git@github.com:prebid/prebid-server.git
cd prebid-server
```

### 2. Add the Fork as a Remote

```bash
git remote add fork git@github.com:showheroes/prebid-server.git
```

### 3. Verify Remotes

```bash
git remote -v
```

Expected output:
```
origin  git@github.com:prebid/prebid-server.git (fetch)
origin  git@github.com:prebid/prebid-server.git (push)
fork    git@github.com:showheroes/prebid-server.git (fetch)
fork    git@github.com:showheroes/prebid-server.git (push)
```

### 4. Fetch All Branches

```bash
git fetch --all
```

## Working with the Fork

### Creating a Feature Branch

Always create feature branches from the `release` branch of the fork:

```bash
git fetch fork
git checkout -b PLT-1234-my-feature fork/release
```

### Syncing Master with Upstream

To keep the fork's `master` branch in sync with upstream (for reference only):

```bash
git checkout master
git pull origin master --rebase
git push fork master
```

> **Note:** This does not affect the `release` branch. See [Upgrading Prebid Server](#upgrading-prebid-server) for how to upgrade to a new upstream version.

## Pull Request Guidelines

### All PRs Must:

1. **Target the `release` branch** on the fork (`showheroes/prebid-server`)
2. **Be opened on the fork**, not the upstream repository
3. Include clear descriptions of changes
4. Pass all CI checks

### Creating a Pull Request

1. Push your feature branch to the fork:
   ```bash
   git push fork PLT-1234-my-feature
   ```

2. Open a PR on GitHub:
   - Go to `https://github.com/showheroes/prebid-server`
   - Click "New Pull Request"
   - Set base branch to `release`
   - Set compare branch to your feature branch

**NOTE: NEVER OPEN A PR ON THE ORIGINAL REPO, ALWAYS BE SURE YOU HAVE SELECTED A FORKED REPO**

## Best Practices

### Do's

- ✅ Always branch from `fork/release` for new features
- ✅ Keep commits atomic and well-described
- ✅ Regularly sync your local branches with the fork
- ✅ Test changes locally before opening a PR
- ✅ Reference related tickets in commit messages and PR descriptions

### Don'ts

- ❌ Never push directly to `master` or `release` branches
- ❌ Never open PRs against the upstream (`prebid/prebid-server`) repository
- ❌ Don't merge upstream changes without proper review
- ❌ Avoid force-pushing to shared branches

## Upgrading Prebid Server

The `release` branch is based on a specific upstream tag. Upgrading to a new version requires careful planning.

### Versioning Strategy

- **Major/Minor upgrades** (e.g., `v3.30.0` → `v3.31.0` or `v4.0.0`): Require syncing with a new upstream tag
- **Patch releases** in our fork (e.g., `v3.30.1` → `v3.30.2`): Our customizations on top of the upstream tag

> All releases from our fork are **patch releases** because we are essentially "patching" the original Prebid Server with our customizations.

### Upgrade Process (Major/Minor)

1. **Sync tags from upstream:**
   ```bash
   git fetch origin --tags
   ```

2. **Identify the target tag:**
   ```bash
   git tag -l 'v*' | sort -V | tail -20
   ```

3. **Create an upgrade branch from the new tag:**
   ```bash
   git checkout -b upgrade/v3.31.0 v3.31.0
   ```

4. **Rebase our customizations on top of the new tag:**
   ```bash
   git pull fork release --rebase
   # Resolve conflicts carefully - preserve our customizations
   ```

5. **Test thoroughly** before merging to `release`

6. **Update this document** with the new current upstream tag

## Conflict Resolution

When rebasing causes conflicts:

1. Identify conflicting files
2. Review both versions carefully
3. Preserve ShowHeroes customizations where applicable
4. Test thoroughly after resolution
5. Document significant conflict resolutions in the PR

## Release Process

Our fork releases are patch versions on top of the upstream tag.

### Tagging Convention

- Upstream tag: `v3.30.0`
- Our releases: `v3.30.1`, `v3.30.2`, etc.

### Creating a Release

Releases are **automated via Jenkins**. Developers should not create tags manually.

**PR Labels:**

| Label | Effect |
|-------|--------|
| `release-candidate` | Builds a Docker image for QA and local testing |
| `patch-release` | Automatically creates a new patch release upon merge |

**Workflow:**

1. Open a PR targeting `release` branch
2. Add `release-candidate` label to trigger a test build for QA
3. Once approved and ready for production, add `patch-release` label
4. Merge the PR — Jenkins will automatically tag and release

## Troubleshooting

### Accidentally Pushed to Upstream

Contact the upstream maintainers immediately and coordinate removal of unintended changes.

### Feature Branch Out of Date

If `release` has been updated while working on a feature, rebase your changes:

```bash
git checkout PLT-1234-my-feature
git pull fork release --rebase
# Resolve any conflicts, then force push
git push fork PLT-1234-my-feature -f
```

## Quick Reference

| Action | Command |
|--------|---------|
| Fetch all | `git fetch --all` |
| New feature branch | `git checkout -b PLT-XXXX-feature fork/release` |
| Push to fork | `git push fork PLT-XXXX-feature` |
| Sync tags from upstream | `git fetch origin --tags` |
| List available tags | `git tag -l 'v*' \| sort -V \| tail -20` |
