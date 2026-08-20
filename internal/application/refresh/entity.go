// Package refresh holds transport-independent board-refresh publication metadata.
package refresh

// Entity is optional identity carried with a board refresh publication.
// Zero value means no enrichment; consumers must fall back to generic copy.
type Entity struct {
	LocalID int64  // cards
	Title   string // cards
	Name    string // sprint / column / tag display name only
}
