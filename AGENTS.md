# Agent develop guidelines

## Version control
- This project uses Jujutsu (`jj`) as the primary VCS workflow.
- Jujutsu change descriptions must be a single concise English sentence with a type prefix, e.g. `feat: ...`, `fixup: ...`, `refactor: ...`, `docs: ...`, `test: ...`, `chore: ...`.
- Always use `jj --no-pager ...` for status/log/show-style inspection commands in agent sessions.

### Best Practice
- Treat workspace as **"clear"** only when the working copy `@` is 
    1. an empty change.
    2. no file modifications in status.
    3. the description is empty.
- Prefer starting edits only from a clear workspace, unless the user explicitly asks to continue on the current non-empty change.
- If the user asks to finish current change, do things like finishing edits, then make a new change to let working copy clear.
- If the user asks to merge changes, squash descendant changes into the target parent along the parent chain, then rewrite the merged change description and leave `@` as a new empty change.
