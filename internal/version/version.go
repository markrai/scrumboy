package version

const Version = "3.39.1"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.2" // 1.2: additive archival state with presence-aware import semantics

// ArchiveExportFormatVersion is the manifest version inside .sbz archives, which add per-project
// image assets. JSON exports keep ExportFormatVersion so image-free backups stay compatible.
const ArchiveExportFormatVersion = "1.3"

// AppName is the application name.
const AppName = "scrumboy"
