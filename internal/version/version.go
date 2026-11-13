package version

const Version = "3.42.0"

// ExportFormatVersion is the version of the backup/export data format.
// Only increment this when the ExportData structure changes in a breaking way.
// This is separate from the app version to maintain export/import compatibility.
const ExportFormatVersion = "1.2" // 1.2: additive archival state with presence-aware import semantics

// ArchiveExportFormatVersion is the manifest version inside .sbz archives, which add per-project
// image assets and (since 1.4) Story and Wall drawings. JSON exports keep ExportFormatVersion so
// image-free, drawing-free backups stay compatible.
const ArchiveExportFormatVersion = "1.4"

const LegacyArchiveExportFormatVersion = "1.3"

func IsSupportedArchiveVersion(v string) bool {
	return v == ArchiveExportFormatVersion || v == LegacyArchiveExportFormatVersion
}

// AppName is the application name.
const AppName = "scrumboy"
