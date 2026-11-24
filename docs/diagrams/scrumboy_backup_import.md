# Backup and import

Scrumboy has two backup layers:

1. **Logical project export/import** through Settings/API.
2. **Whole-instance filesystem backup** of DATA_DIR plus external secrets for disaster recovery.

This page covers the logical layer. See `scrumboy_deployment_ops.md` for full-instance recovery.

```mermaid
flowchart TB
  Export["GET /api/backup/export"]
  Shape{archive-only content needed?}
  JSON["JSON format 1.2"]
  SBZ[".sbz ZIP manifest format 1.4"]
  Preview["POST /api/backup/preview"]
  Import["POST /api/backup/import"]
  Mode{import mode}
  Replace[Replace]
  Merge[Merge]
  Copy[Create copy]

  Export --> Shape
  Shape -->|no images/drawings/ink| JSON
  Shape -->|images or drawing/ink content| SBZ
  JSON --> Preview
  SBZ --> Preview
  Preview --> Import --> Mode
  Mode --> Replace
  Mode --> Merge
  Mode --> Copy
```

JSON format 1.2 carries portable project/story state such as workflow, tags, priorities, archive state, due dates, custom fields and related project content. Publication state is intentionally not transferred.

Archive format **1.4** is used when archive-only content must travel with the project. It can include:

- inline note images under `assets/`
- standalone/story drawing data
- Wall drawing data
- Notes ink

Older archive 1.3 remains readable as a legacy image archive, but it cannot represent the drawing/ink additions introduced in 1.4.

## Archive structure

```mermaid
flowchart LR
  SBZ["backup.sbz"]
  Manifest["manifest.json version 1.4"]
  Assets["assets/id.ext"]
  Projects[project/story metadata]
  Draw[story + Wall drawings and Notes ink]

  SBZ --> Manifest
  SBZ --> Assets
  Manifest --> Projects
  Manifest --> Draw
```

Imported image IDs are remapped and embedded note destinations are rewritten only where they represent Scrumboy-managed images. Merge can reuse identical target-project bytes. Import preflight rejects incomplete/corrupt archives before committing project state.

Replace, Merge and Create copy preserve the existing mode semantics. Replace requires explicit confirmation and must not leave the database half-mutated if asset restoration fails.

## Native import flow

```mermaid
flowchart TB
  Preview[PreviewImport / archive preflight]
  Mode{mode}
  Validation[Validate schema identities priorities drawings assets]
  Tx[project-writer serialized transaction]
  Assets[asset staging/remap]
  Commit[commit project state]
  Finalize[finalize asset publication]

  Preview --> Validation --> Mode
  Mode --> Tx
  Tx --> Assets --> Commit --> Finalize
```

## Trello import

Trello JSON uses its own conversion pipeline and does not share the generic native import transform.

```mermaid
flowchart LR
  Trello[Trello board JSON]
  Build[trelloimport.BuildImportBundle]
  Preview[preview warnings and hard errors]
  Gate{hard errors empty?}
  Store[ImportTrelloProject]

  Trello --> Build --> Preview --> Gate
  Gate -->|yes| Store
  Gate -->|no| Reject[reject before mutation]
```

Closed Trello cards become archived Scrumboy stories; closed lists are a separate concept and retain their closed-list labeling/mapping. Trello members are preserved as import context rather than silently becoming Scrumboy assignees.

Logical project imports do not restore host-level GitHub credentials, wallpapers, instance audit state, or encryption secrets.
