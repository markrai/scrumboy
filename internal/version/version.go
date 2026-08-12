package version

const Version = "3.31.3"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.1" // 1.1: additive fields, including doneAt and presence-aware priority data

// AppName is the application name.
const AppName = "scrumboy"
