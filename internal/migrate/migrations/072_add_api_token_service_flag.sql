-- Migration 072: mark a normal user-owned API token for service/automation use
--
-- A service token is an ordinary user-owned token: while its user exists it
-- authenticates as that user with that user's permissions. The flag only affects
-- user deletion: DeleteUser copies the token's metadata into the archive (migration
-- 073), and then the api_tokens row cascade-deletes with the user like every other
-- token, in the same transaction. Existing and unflagged tokens default to personal
-- (0) and are simply deleted with their owner.

ALTER TABLE api_tokens ADD COLUMN is_service INTEGER NOT NULL DEFAULT 0;
