package version

const Version = "3.36.5"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.2" // 1.2: additive archival state with presence-aware import semantics

// AppName is the application name.
const AppName = "scrumboy"
