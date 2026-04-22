package version

const Version = "3.14.1"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.1" // 1.1: added doneAt to todo export for completion history

// AppName is the application name.
const AppName = "scrumboy"
