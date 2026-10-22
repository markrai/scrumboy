-- Migration 078: inline image assets for story notes
--
-- project_assets holds metadata for immutable image files stored at
-- DATA_DIR/assets/{uuid[0:2]}/{uuid}.{png|jpg|gif}. The uuid is minted by the server, never
-- reused, and never derived from client or archive input. Files are written before their row is
-- inserted, and unlinked only after the row deletion that makes them unreachable has committed.
--
-- todo_assets records which assets a story's notes embed. It is derived on the server from the
-- note body inside the same transaction as every body write, so it is never trusted from clients.
--
-- unreferenced_at is NULL exactly when at least one todo_assets row exists for the asset. It is set
-- to the insert time for a new asset (the draft retention clock) and maintained by the triggers
-- below on every reference insert/delete, including rows removed by ON DELETE CASCADE.

CREATE TABLE IF NOT EXISTS project_assets (
  id               INTEGER PRIMARY KEY,
  uuid             TEXT NOT NULL UNIQUE CHECK (
                     typeof(uuid) = 'text'
                     AND length(uuid) = 36
                     AND uuid GLOB '????????-????-4???-[89ab]???-????????????'
                     AND uuid NOT GLOB '*[^0-9a-f-]*'
                     AND length(replace(uuid, '-', '')) = 32
                   ),
  project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  uploader_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  mime_type        TEXT NOT NULL CHECK (mime_type IN ('image/png', 'image/jpeg', 'image/gif')),
  size_bytes       INTEGER NOT NULL CHECK (size_bytes > 0),
  width            INTEGER NOT NULL CHECK (width > 0),
  height           INTEGER NOT NULL CHECK (height > 0),
  sha256           TEXT NOT NULL CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
  original_name    TEXT NOT NULL DEFAULT '' CHECK (length(original_name) <= 255),
  created_at       INTEGER NOT NULL,
  unreferenced_at  INTEGER
);

-- Serves per-project dedupe by content and per-project listing (export, deletion cleanup).
-- uuid needs no extra index: UNIQUE already creates one.
CREATE INDEX IF NOT EXISTS idx_project_assets_project_sha256
  ON project_assets(project_id, sha256);

-- Serves the garbage-collection candidate scan; referenced assets stay out of the index.
CREATE INDEX IF NOT EXISTS idx_project_assets_gc
  ON project_assets(unreferenced_at, id)
  WHERE unreferenced_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS todo_assets (
  todo_id    INTEGER NOT NULL REFERENCES todos(id) ON DELETE CASCADE,
  asset_id   INTEGER NOT NULL REFERENCES project_assets(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (todo_id, asset_id)
) WITHOUT ROWID;

-- Serves "is this asset referenced" and the public "referenced by an active story" join.
CREATE INDEX IF NOT EXISTS idx_todo_assets_asset
  ON todo_assets(asset_id, todo_id);

-- Defense in depth: a story may only reference assets of its own project.
CREATE TRIGGER IF NOT EXISTS trg_todo_assets_same_project
BEFORE INSERT ON todo_assets
WHEN (SELECT project_id FROM todos WHERE id = NEW.todo_id) IS NOT
     (SELECT project_id FROM project_assets WHERE id = NEW.asset_id)
BEGIN
  SELECT RAISE(ABORT, 'todo_assets cross-project reference');
END;

CREATE TRIGGER IF NOT EXISTS trg_todo_assets_referenced
AFTER INSERT ON todo_assets
BEGIN
  UPDATE project_assets
  SET unreferenced_at = NULL
  WHERE id = NEW.asset_id AND unreferenced_at IS NOT NULL;
END;

CREATE TRIGGER IF NOT EXISTS trg_todo_assets_unreferenced
AFTER DELETE ON todo_assets
WHEN NOT EXISTS (SELECT 1 FROM todo_assets WHERE asset_id = OLD.asset_id)
BEGIN
  UPDATE project_assets
  SET unreferenced_at = CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)
  WHERE id = OLD.asset_id;
END;

CREATE TRIGGER IF NOT EXISTS trg_project_assets_immutable
BEFORE UPDATE OF uuid, project_id, mime_type, sha256, size_bytes, width, height ON project_assets
WHEN NEW.uuid IS NOT OLD.uuid
  OR NEW.project_id IS NOT OLD.project_id
  OR NEW.mime_type IS NOT OLD.mime_type
  OR NEW.sha256 IS NOT OLD.sha256
  OR NEW.size_bytes IS NOT OLD.size_bytes
  OR NEW.width IS NOT OLD.width
  OR NEW.height IS NOT OLD.height
BEGIN
  SELECT RAISE(ABORT, 'project_assets identity is immutable');
END;
