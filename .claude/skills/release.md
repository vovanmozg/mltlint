---
name: release
description: Tag and push a new release to trigger GitHub Actions build
argument-hint: "<version, e.g. v1.0.0>"
allowed-tools: Bash(git:*), Bash(go:*), Bash(gh:*)
---

# Release

Create a git tag and push it to trigger the GitHub Actions release workflow.

## Steps

1. **Parse version** from `$ARGUMENTS`. Must match `vX.Y.Z`. If not provided or invalid, ask the user.

2. **Check preconditions:**
   - `git status` is clean (no uncommitted changes)
   - Tag does not already exist (`git tag -l <version>`)
   - Tests pass (`go test ./...`)

3. **Show what will happen:**
   - Version: the tag
   - Commits since last tag: `git log $(git describe --tags --abbrev=0 2>/dev/null)..HEAD --oneline` (or all commits if no prior tag)
   - Target branch: current branch name

4. **Ask user to confirm** before proceeding.

5. **Execute:**
   ```
   git tag <version>
   git push origin <version>
   ```

6. **Report result** with link to the GitHub Actions run: `gh run list --limit 1`
