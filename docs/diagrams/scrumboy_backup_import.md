# Backup and import

Project export/restore and Trello JSON import use separate HTTP and store paths.

```mermaid
flowchart TB
  Export["GET /api/backup/export"]
  PreviewN["POST /api/backup/preview"]
  ImportN["POST /api/backup/import"]
  PreviewT["POST /api/import/trello/preview"]
  ImportT["POST /api/import/trello"]

  Export --> JSON[Scoped JSON ExportData]
  PreviewN --> PrevStore["store.PreviewImport"]
  ImportN --> Mode{importMode}
  Mode --> Replace[replace plus confirmation REPLACE]
  Mode --> Merge[merge]
  Mode --> Copy[copy]
  Merge --> Presence["Priority presence: absent preserve, null clear, string assign"]
  Presence --> Invariant["In-transaction project/todo priority anti-join"]
  Replace --> Native["store.ImportProjectsWithTarget"]
  Merge --> Native
  Copy --> Native

  PreviewT --> Bundle["trelloimport.BuildImportBundle"]
  Bundle --> TPrev[Preview warnings and hardErrors]
  ImportT --> Bundle2["trelloimport.BuildImportBundle"]
  Bundle2 --> Gate{hardErrors empty?}
  Gate -->|yes| TrelloStore["store.ImportTrelloProject"]
  Gate -->|no| Reject[validation error]
```

Native backup: preview via `PreviewImport`; mutate via `ImportProjectsWithTarget` (`replace` / `merge` / `copy`). Replace requires `confirmation: "REPLACE"`.

Export format 1.1 uses syntactic presence for priorities without a version
bump. New exports write `priorityTiers` for every project (`[]` means the four
canonical defaults) and `priorityKey` for every todo (`null` means no
assignment). A legacy 1.1 backup may omit these fields. On a matched-project
merge, omitted project definitions and todo assignments are preserved;
explicit arrays replace definitions, explicit todo `null` clears, and a string
assigns. Replacement and merge run under project-writer serialization and
abort if any effective non-null todo key would not resolve in the project.

Trello import uses dedicated `ImportTrelloProject` (not the generic `ImportProjects` path). Preview and import both use `trelloimport.BuildImportBundle`. Import rejects when preview `hardErrors` is non-empty. Body size is capped separately (`MaxTrelloImportBody`).

## Trello transform

```mermaid
flowchart LR
  TrelloJSON[Trello board JSON]
  Map[Lists cards labels checklists]
  Notes["Member names into todo body"]
  Proj["New project via ImportTrelloProject"]

  TrelloJSON --> Map --> Notes --> Proj
```

Trello members do **not** become Scrumboy assignees automatically; member information is preserved in note text where applicable (see Trello import warnings).

Backup and Trello import paths do **not** append import audit events. Do not treat imports as audited actions unless product code adds that later.
