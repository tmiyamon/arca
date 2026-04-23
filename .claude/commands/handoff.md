---
description: Write a session handoff memory and emit a switch-ready message
---

Write an end-of-session handoff for the Arca project, then produce a brief switch message so the user can start a fresh session cleanly. Do not make code changes — this is a meta task.

## Steps

1. **Gather session state**
   - Run `git log --format="%h %s" origin/main..HEAD` to list new commits.
   - Run `git status -s` and note any uncommitted files. Pay attention to user in-progress files (especially `examples/todo/main.arca`) that must stay out of commits.
   - Skim recent feedback from the user during the session for preferences or decisions worth preserving.

2. **Write the handoff memory** under `~/.claude/projects/-Users-tmiyamon-src-arca/memory/project_session_handoff_<YYYY>_<MM>_<DD>[_<slot>].md` with this structure (Japanese body is fine; section headings in English):
   ```markdown
   ---
   name: Session handoff <YYYY-MM-DD>[ <slot>] — <short theme>
   description: End-of-session snapshot. <One-line summary of what landed and what is next.>
   type: project
   ---

   ## What happened this session
   Narrative of commits grouped by theme, with slice IDs when applicable.

   ## Why / deferred
   Items that were scoped out, blocked, or revealed as bigger than expected.

   ## New plan / next slice
   Concrete next steps with priority. Reference ideas.md entries by link.

   ## Git state at session end
   - Branch / commits-ahead count
   - Uncommitted files and whether they are user-in-progress
   - Anything unusual (stash, untracked, etc.)

   ## Reading order when reopening
   1. This file.
   2. Specific `decisions/ideas.md` entries or other memories to prime on.
   3. Code entry points (file:line) to re-find fast.

   ## User preference reminders
   Point at the feedback_*.md memories that were most relevant this session.
   ```

3. **Update MEMORY.md** by adding a one-line entry for the new handoff at the bottom of the handoff cluster.

4. **Emit the switch-ready message** as the final user-facing reply. Format:
   ```
   **セッション切り替え準備完了**

   - handoff: `<filename>`
   - 主要成果: <1-line summary>
   - 次回着手: <1-line next step>
   - 再開時コマンド: このファイルを読んで再開してください → `<path>`
   ```

## Reminders

- Do not reference uncommitted files as motivation inside commits (per `feedback_commit_structure.md`).
- Do not duplicate content that lives in a linked design doc.
- Handoff memory can be longer than a commit — detail is fine when it adds signal.
- If the session contained deferred designs, make sure they are recorded in `decisions/ideas.md` first, then point at them from the handoff.
