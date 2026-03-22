-- Migration 030: Add done_at for completion analytics
-- done_at = "last completion time" (Unix ms). Set only when status transitions into DONE; never cleared on reopen.
-- Nullable: non-DONE rows may have NULL (never completed) or NOT NULL (reopened; stores last completion).

ALTER TABLE todos ADD COLUMN done_at INTEGER;

-- Best-effort backfill: pre-done_at completion times are derived from updated_at.
-- For DONE items that were edited after being completed, this may be approximate.
-- New completions (post-migration) will have exact done_at from the write path.
UPDATE todos SET done_at = updated_at WHERE status = 4 AND done_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_todos_assignee_status_done_at ON todos(assignee_user_id, status, done_at);
