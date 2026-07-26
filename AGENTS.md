# Agent develop guidelines

## Version control

- This project uses Jujutsu (`jj`) as the primary VCS workflow.
- Jujutsu change descriptions must be a single concise English sentence with a type prefix, e.g. `feat: ...`, `fixup: ...`, `refactor: ...`, `docs: ...`, `test: ...`, `chore: ...`.
- Always use `jj --no-pager ...` for status/log/show-style inspection commands in agent sessions.
- Do not commit, push, tag, or publish releases unless the user explicitly asks.

### Best Practice

- Treat workspace as **"clear"** only when the working copy `@` is
  1. an empty change.
  2. no file modifications in status.
  3. the description is empty.
- Prefer starting edits only from a clear workspace, unless the user explicitly asks to continue on the current non-empty change.
- If the user asks to finish current change, do things like finishing edits, then make a new change to let working copy clear.
- If the user asks to merge changes, squash descendant changes into the target parent along the parent chain, then rewrite the merged change description and leave `@` as a new empty change.

## CI

Daily test automation lives in `.github/workflows/test.yml`.

- Trigger: every push to `dev`.
- Runs: `make test` and `make test-functional` only.

## Release

Release automation lives in `.github/workflows/release.yml` and `.github/workflows/release-cleanup.yml`. Do not change release behavior without updating this section and the workflows together.

This repo uses **Jujutsu (`jj`)**, not a colocated Git working tree. Do not use raw `git tag` / `git push` from the project directory unless you have exported through `jj git export` first.

### Agent checklist (before every release)

Do not push, tag, or trigger a release until all of the following are done:

1. **Confirm with the user**
   - Tag / version (for example `v0.0.1-alpha`)
   - Target branch bookmark (for example `dev` for pre-release, `main` for formal release)
   - Whether this is a **new tag** or **republishing an existing tag**

2. **Finish changes and leave the workspace clear**
   - Every intended edit must be saved in a described change (`jj describe -m "..."`).
   - After describing, `@` should be an **empty change** with no file modifications and no description. Verify with `jj --no-pager status`.
   - If multiple fixup changes should ship as one release, squash them onto the target branch parent before tagging (see below).
   - The tagged revision must be the commit that contains all release content; move the branch bookmark to it before pushing.

3. **Run tests locally and confirm they pass**
   ```sh
   make test
   make test-functional
   ```
   CI runs the same targets. Do not release if either fails. On Windows, run the equivalent `go test` commands if `make` is unavailable, but prefer a Linux-like environment when functional tests matter.

4. **Review what will ship**
   ```sh
   jj --no-pager log -r '<branch-bookmark>' -n 5
   jj --no-pager bookmark list
   jj --no-pager tag list
   ```
   Restate tag, branch, and top commit message to the user immediately before pushing.

Only proceed with push/tag steps after explicit user approval of the version and branch.

### Jujutsu release workflow

Typical flow for publishing (or republishing) from `dev`:

```sh
# 1. Point the branch bookmark at the release commit
jj bookmark set dev -r '@'          # or: jj bookmark set dev -r '<change-id>'

# 2. Create or move the release tag
jj tag set v0.0.1-alpha -r dev --allow-move

# 3. Push the branch
jj git push --remote origin --bookmark dev

# 4. Export jj state to the backing Git repo and push the tag
jj git export
cd .jj/repo/store/git
git push --force origin refs/tags/v0.0.1-alpha
```

Notes:

- Use `--allow-move` when republishing an existing tag to a new commit.
- `--force` on the tag push is required when moving a tag; pre-releases and drafts are designed to be replaced this way.
- Remote name is `origin` (`https://github.com/Cyrisub/vaps.git`). There is also an `internal` remote; do not push releases there unless the user asks.
- After `jj describe`, the described change becomes immutable and `jj` opens a new empty `@` on top. That empty `@` is the expected “clear workspace” state.
- To squash fixups into one release commit before tagging:
  ```sh
  jj squash --into dev
  jj describe -m "feat: ..."   # rewrite the squashed change message if needed
  ```

### What happens after push

- Pushing `dev` triggers the **Test** workflow on that commit.
- Pushing the tag triggers the **Release** workflow, which waits for a successful **Test** run on the **same commit**, then builds and publishes GitHub release assets.
- Monitor: https://github.com/Cyrisub/vaps/actions

### Trigger

- Push a SemVer tag to GitHub, for example `v1.0.0` or `v1.0.0-alpha.1`.
- Tag format: `vMAJOR.MINOR.PATCH`, with optional `-prerelease` and `+build` suffixes.
- The workflow validates the tag, waits for a successful `Test` workflow run on the tagged commit, then publishes a GitHub release.
- Delete a release SemVer tag on GitHub and `release-cleanup.yml` deletes the matching GitHub release entry if one exists. Non-release tags are ignored. GitHub does not emit `delete` events when more than three tags are deleted in one operation.

Only push tags when the user explicitly asks.

### Release type by branch

Release type is derived from remote branches that contain the tagged commit at publish time. Priority is `main`/`master` over `dev`.

| Branch containing tag | GitHub release type |
|-----------------------|---------------------|
| `main` or `master` | Latest (formal release) |
| `dev` | Pre-release |
| Any other branch | Draft |

Notes:

- Classification uses the branch graph at tag push time, not where the tag was created locally.
- After `dev` is merged into `main`, older `dev` commits become reachable from `main`; a later tag on such a commit becomes a formal release.

### Duplicate tags

| Release kind | Same tag already exists |
|--------------|-------------------------|
| Pre-release or Draft | Delete the existing GitHub release, then republish |
| Formal release | Fail the workflow; duplicate formal releases are forbidden |

Republishing the same tag replaces release metadata and assets for pre-releases and drafts only.

### Release title and body

- Title: the tag itself, for example `v0.0.1`.
- Formal releases: include a `## Summary` section with commits since the previous tag.
- All releases: include a `## Artifacts` section listing SHA256 checksums for every uploaded archive so republished builds with the same tag can be compared.
- Do not add separate release metadata blocks such as tag, commit, or release kind.

### Artifacts

Platforms:

- `linux/amd64`, `linux/arm64`
- `darwin/amd64`, `darwin/arm64`
- `windows/amd64`

Naming:

- `vaps-<version>-<os>-<arch>.tar.gz` on Linux and macOS
- `vaps-<version>-<os>-<arch>.zip` on Windows

Packaging rules:

- Upload platform binary archives plus a standalone Unix `install.sh` bootstrap asset; do not include source trees in custom release assets.
- Each platform archive contains the `vaps` binary plus bundled `start`/`update` helper scripts, default `config.toml`, and `vaps-install.conf`.
- `install.sh` is published as its own release asset (not inside the archives). Release publish substitutes `GITHUB_REPO` and `CHANNEL` to match that release.

GitHub limitation:

- GitHub always attaches automatic `Source code (zip)` and `Source code (tar.gz)` downloads to every release. This cannot be disabled from the workflow.
