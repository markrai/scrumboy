package project

import "scrumboy/internal/store"

// AssetFileRemover removes image files whose rows were deleted by a committed transaction.
// Removal is best-effort; the asset janitor's orphan sweep reclaims anything left behind.
type AssetFileRemover interface {
	RemoveFiles(refs []store.AssetFileRef)
}

// removeDeletedProjectAssets runs only after DeleteProject returned success, so files are never
// removed for a deletion that did not commit.
func removeDeletedProjectAssets(remover AssetFileRemover, refs []store.AssetFileRef) {
	if remover == nil || len(refs) == 0 {
		return
	}
	refs = append([]store.AssetFileRef(nil), refs...)
	if len(refs) < 50 {
		remover.RemoveFiles(refs)
		return
	}
	go remover.RemoveFiles(refs)
}
