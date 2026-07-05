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

### Trigger

- Push a SemVer tag to GitHub, for example `v1.0.0` or `v1.0.0-beta.1`.
- Tag format: `vMAJOR.MINOR.PATCH`, with optional `-prerelease` and `+build` suffixes.
- The workflow validates the tag, waits for a successful `Test` workflow run on the tagged commit, then publishes a GitHub release.
- Delete a release SemVer tag on GitHub and `release-cleanup.yml` deletes the matching GitHub release entry if one exists. Non-release tags are ignored. GitHub does not emit `delete` events when more than three tags are deleted in one operation.

Example:

```sh
git tag v1.0.0
git push origin v1.0.0
```

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

- `vaps-<version>-<os>-<arch>.tar.gz` on Linux
- `vaps-<version>-<os>-<arch>.zip` on macOS and Windows

Packaging rules:

- Upload binary archives only; do not include source trees in custom release assets.
- Each archive contains a single compiled `vaps` binary.

GitHub limitation:

- GitHub always attaches automatic `Source code (zip)` and `Source code (tar.gz)` downloads to every release. This cannot be disabled from the workflow.
