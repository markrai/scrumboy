package version

// Version is the semantic version of the application.
//
// Invariant: Every deploy MUST bump this value. No deploy without a version bump.
// Asset URLs in HTML (app.js, styles.css) include this version for cache busting;
// the displayed version and deployed UX must always match.
//
// Convention: Update when releasing (e.g., "1.0.0", "1.1.0"); match git tags
// (e.g., tag "v1.0.0" should have Version = "1.0.0").
const Version = "3.11.10"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.1" // 1.1: added doneAt to todo export for completion history

// AppName is the application name.
const AppName = "scrumboy"
