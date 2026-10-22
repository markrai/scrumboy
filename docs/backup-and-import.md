# Backup, export, and import

Scrumboy can export and import **project data as JSON** from Settings → Backup (and the matching HTTP APIs). Use this for moving or restoring boards, todos, tags, links, and related project content between instances or as a logical backup.

This path is **separate** from a full instance disaster-recovery backup of the server’s data directory.

## JSON export is not a full disaster-recovery backup

JSON export/import covers Scrumboy **project** data in a portable backup file. It is **not** a complete `DATA_DIR` disaster-recovery backup.

JSON export does **not** include:

- Uploaded wallpaper files under `DATA_DIR/user-wallpapers/`
- Images in story notes under `DATA_DIR/assets/` - project exports with images use the `.sbz` archive format below instead of JSON, so they *do* carry the images of exported stories
- `audit_events`
- General user preferences as a full restore unit
- Other file-backed instance state outside the scoped project export

For whole-instance backup and restore (SQLite, WAL/SHM, wallpapers, note images, encryption key when used), see [recovery.md](recovery.md#instance-backup-and-restore) and [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md).

## Export scope

JSON export is **scoped**. Choose Full or Single-project in Full mode deployments.

### Full

In Full mode, Full export includes:

- Durable projects where the current user is a **Maintainer**
- Temporary/expiring boards created by the current user

Viewer or Contributor membership alone does **not** include a durable project in Full export.

### Single project

Single-project export contains one selected board/project only.

### Anonymous mode

Anonymous mode uses **single-board** export rather than Full export.

## Import modes

When importing a backup JSON, choose how it is applied.

### Replace

Replace is the destructive “make this scope match the backup” option:

- Deletes every project in your **current export scope** (the same set Full or Single export would cover for you)
- Creates projects from the backup so that scope matches the file
- Requires typing the confirmation **`REPLACE`**
- **Not available** in Anonymous mode

### Merge

In Full mode, Merge matches each backup project by **slug**:

- If a project with that slug already exists and you are a **Maintainer** on it, Scrumboy updates that project’s imported data (including todos, tags, and links) to match the backup
- Otherwise Scrumboy creates a new project where appropriate

Anonymous-mode Merge behavior is described below (it does not match durable projects the same way).

### Create copy

Create copy always creates **new** projects for every project in the backup:

- Existing projects are not overwritten
- Slugs are made unique when needed (for example `name-imported-2`, `name-imported-3`)

## Anonymous-mode import behavior

In Anonymous mode:

- **Full-scope** import is not allowed
- Import targets the **current board**: todos and tags from the backup are added to that board
- **Replace** is not available
- **Merge** does not match existing durable projects; when Merge would otherwise create or update projects by slug, Anonymous mode treats imported projects as **new** (same outcome as Create copy)

## Publication state is not transferred

Portable JSON exports never carry board publication state: the export
format has no publication field, so an export cannot leak a public board
into another instance as public.

- **Create copy** and **Replace** create new projects, which always start
  private (`public_view_enabled` defaults to off).
- **Merge** into an existing project leaves that project's publication state
  untouched. Importing a backup can neither publish nor unpublish the merge
  target; re-publish explicitly after import if needed.

A full /data backup (the SQLite file) preserves the state like any other
database column. See [public-boards.md](public-boards.md).

## Backup format compatibility

New exports are written as format **1.2**, which adds additive story archival state. Imports
accept both **1.1** and **1.2**; any other version string is rejected. Older applications that
only understand 1.1 should reject 1.2 files rather than silently dropping archive state, so
export a backup from the older version before downgrading.

A payload labeled 1.1 that contains `archivedAt` is rejected as mislabeled newer data;
format 1.1 never acquires archival semantics from an unknown field.

`archivedAt` values are validated before anything is written: a negative timestamp is rejected
(it would otherwise create a story the archive cursor could never page past), and so is one
implausibly far in the future, which is the shape a seconds- or nanoseconds-instead-of-
milliseconds mistake takes. A small allowance covers ordinary clock skew between the
exporting and importing machines.

New exports explicitly include:

- Project `priorityTiers` — `[]` means the canonical/default priority tiers
- Todo `priorityKey` — `null` means no priority assigned
- Todo `archivedAt` — a Unix-millisecond timestamp for archived stories, or explicit `null` for active stories
- Todo `dueAt` — a Unix-millisecond due date, **omitted** when the story has none (so exports that use no due dates are unchanged); an importing server that predates due dates rejects the unknown field. `dueAt` must be after the epoch and before year 2100

During **matched-project merge**:

- **Absent** legacy priority fields preserve the target project’s existing tier definitions or todo assignments
- An explicit todo `priorityKey` of `null` **clears** that todo’s assignment
- An absent `archivedAt` preserves the target story’s archive state; explicit `null` clears it, and a timestamp sets it
- An absent `dueAt` preserves the target story’s due date; a value replaces it and starts a new reminder cycle (there is no explicit-`null` clear on merge)
- Explicit `priorityTiers` arrays replace definitions; a string `priorityKey` assigns against the effective project tier set

Outside matched-project merge — **Create copy**, **Replace**, import into an existing
anonymous board, and merge branches that create a new story — there is no target state to
preserve, so an absent `archivedAt` simply creates an **active** story. A 1.1 payload
therefore always imports as active, exactly as it did before archival existed.

Import commits only when every non-null todo priority key resolves to a tier in the same project.

## Backups with images (`.sbz`)

When [inline images](inline-images.md) are used, an export whose stories embed images downloads as a **`.sbz`** archive instead of JSON. Exports without images stay JSON format 1.2, readable by older Scrumboy versions.

A `.sbz` file is a ZIP containing:

- `manifest.json` - the same backup data as a JSON export, format **1.3**, with each project listing its images (`assets`: id, type, size, dimensions, SHA-256)
- `assets/<id>.png|jpg|gif` - the image bytes

Only images embedded by an exported story (active or archived) are included; unsaved drafts are not. The image list is taken from the exported note text itself, so every image the backup's notes embed is in the archive, even while others edit stories or right after a downgrade. Every image file is checked before the download starts, so a backup with a missing image fails with an error instead of downloading an incomplete archive. In the rare case that a story is edited and its image removed during the export, the export fails and can simply be repeated. A note that embeds an image URL that never belonged to its project (typed by hand, or copied from another board) shows "Image unavailable" and is exported as text only.

Importing a `.sbz` (Settings → Backup accepts `.json` and `.sbz`):

- Requires inline images to be enabled on the importing server.
- Works in **Replace**, **Merge** and **Create copy** with the same rules and confirmation as JSON imports. The preview also shows the number of images.
- The archive is uploaded twice: once for the preview and once for the import.
- Imported images always get **new ids**. Note text is rewritten only where an image is embedded. Prose, code and ordinary links that mention an image URL are left unchanged, so such links keep the old id and will not resolve after **Create copy** or **Replace**.
- **Merge** reuses an existing image of the target project when the bytes are identical, so merging a project's own backup does not duplicate images.
- Image bytes are re-validated and must match the manifest checksums. A failed import leaves existing projects and images unchanged.
- Every image a story embeds must be restored. If any cannot be, the preview and the import fail before anything is written, including in **Replace**. This happens for some hand-edited archives, for a note over 1 MiB that embeds an image, and for a reference-style image whose definition is also used by an ordinary link (for example `![Shot][a]` and `[Open][a]` sharing `[a]: /api/assets/…`), because rewriting it would retarget the link. Give the link its own definition and export again.
- One archive per user is processed at a time, two across the server, and each user may send 10 archive previews or imports per 10 minutes. Busy requests are refused before the upload is stored; retry shortly.
- Storage budgets apply. A **Replace** needs free budget for the imported images, because the old projects' images are released only when the replacement completes.
- Limits: 512 MB upload, 1 GB total uncompressed, 5,000 entries, 10 MB per image. There is no compression-ratio rule.
- A JSON file labeled 1.3, or a 1.1/1.2 JSON file listing images, is rejected: import the `.sbz` instead.

A Replace import that was interrupted by a crash leaves invisible staging data; Scrumboy removes it, and its image files, after the next restart.

In the Android app, `.sbz` backups cannot be exported or imported. The app shows a message instead, and JSON backups over 8 MB are refused there too. Use a web browser for these backups.
