# Backup, export, and import

Scrumboy can export and import **project data as JSON** from Settings → Backup (and the matching HTTP APIs). Use this for moving or restoring boards, todos, tags, links, and related project content between instances or as a logical backup.

This path is **separate** from a full instance disaster-recovery backup of the server’s data directory.

## JSON export is not a full disaster-recovery backup

JSON export/import covers Scrumboy **project** data in a portable backup file. It is **not** a complete `DATA_DIR` disaster-recovery backup.

JSON export does **not** include:

- Uploaded wallpaper files under `DATA_DIR/user-wallpapers/`
- `audit_events`
- General user preferences as a full restore unit
- Other file-backed instance state outside the scoped project export

For whole-instance backup and restore (SQLite, WAL/SHM, wallpapers, encryption key when used), see [diagrams/scrumboy_deployment_ops.md](diagrams/scrumboy_deployment_ops.md).

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

## Backup format compatibility

Backup format **1.1** remains backward-compatible with backups created before priorities existed.

New exports explicitly include:

- Project `priorityTiers` — `[]` means the canonical/default priority tiers
- Todo `priorityKey` — `null` means no priority assigned

During **matched-project merge**:

- **Absent** legacy priority fields preserve the target project’s existing tier definitions or todo assignments
- An explicit todo `priorityKey` of `null` **clears** that todo’s assignment
- Explicit `priorityTiers` arrays replace definitions; a string `priorityKey` assigns against the effective project tier set

Import commits only when every non-null todo priority key resolves to a tier in the same project.
