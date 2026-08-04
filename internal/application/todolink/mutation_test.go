package todolink

import "scrumboy/internal/store"

var _ SourceLookupStore = (*store.Store)(nil)
var _ MutationStore = (*store.Store)(nil)
