package sprint

import "scrumboy/internal/store"

var _ TransitionStore = (*store.Store)(nil)
var _ DeletionStore = (*store.Store)(nil)
