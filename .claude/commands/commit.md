---
description: Create a commit following Arca project conventions
---

Create a commit for the staged or to-be-staged changes, following the project's commit conventions distilled from `feedback_*.md` memories. Do not push.

## Steps

1. **Survey current state**
   - Run `git status` and `git diff` (and `git diff --staged` if anything is already staged) to see what is changing.
   - Run `git log --format="%h %s%n%b%n---" -8` to refresh on recent message style — title shape, body structure, suffixes — before writing anything new.
   - **Count body lines** of the most similar-scope commits (e.g. recent slice landings for a slice landing, design refinements for a design refinement). Target their median, not the upper bound of any general range. When in doubt, err short.
   - If the diff includes user-in-progress files (especially `examples/todo/main.arca`), exclude them from staging unless the user explicitly asks otherwise.

2. **Check doc sync** (per `feedback_always_update_docs.md` / `feedback_doc_sync.md`)
   - For code changes: does `CLAUDE.md`, `SPEC.md`, `DESIGN.md`, or a `decisions/*.md` entry need updating?
   - For design-only changes: is the corresponding `decisions/*.md` entry present and current?
   - For changes that affect future-session resumption: does any `~/.claude/projects/-Users-tmiyamon-src-arca/memory/*.md` need updating?
   - If updates are needed, edit them now and include in the same commit. Don't split into a separate "docs sync" commit.

3. **Stage explicitly**
   - Add files by name. Avoid `git add -A` / `git add .` to prevent picking up untracked binaries or secrets.

4. **Write the message** (per `feedback_commit_structure.md` / `feedback_commit_msg_brevity.md` / `feedback_consistency.md`)
   - **Title**: imperative, < 70 chars. Slice ID in `(B1a)` form at tail when applicable. `as idea` suffix for design-only commits with no code change. Match phrasing of similar prior commits — survey first.
   - **Body**: problem-first. Open with the situation / gap / prior decision being refined, then the new direction. Match the median body line count surveyed in step 1, wrapped at ~65 chars. Don't pad with explanatory phrases that restate the title or describe consumers ("for X to consume", "directing the user to Y"). Don't duplicate content already in a linked design doc; reference it (`Per decisions/ffi.md 2026-05-02 (refined) Synthetic Builder, ...`) and only summarise what's new.
   - **Never reference uncommitted state** (e.g. "preparing for X" where X isn't in this commit).
   - **Optional next-step trailer**: one line if it adds signal. No "Docs synced" / "Tested locally" / similar filler trailers.
   - **No Co-Authored-By trailer** (per `feedback_no_coauthor.md`).

5. **Commit via HEREDOC** so newlines render correctly:
   ```
   git commit -m "$(cat <<'EOF'
   <title>

   <body>
   EOF
   )"
   ```

6. **Verify** with `git status` and `git log -1 --format='%B'`. Read the rendered message back and confirm length, structure, and absence of forbidden trailers match neighbors.

## Reminders

- Small fix right after the previous commit (typo, formatting, missed file) → `git commit --amend --no-edit` or `--amend` with edited message, not a new commit (`feedback_amend_fixes.md`, `feedback_commits.md`).
- A change touching many files because of one structural shift is one commit, not N. A change touching many files because of N unrelated fixes is N commits.
- Do not push. Do not run destructive operations (`reset --hard`, force push). The user pushes when they are ready.
- If unsure whether docs need updating, ask the user — don't guess and don't skip the check.
