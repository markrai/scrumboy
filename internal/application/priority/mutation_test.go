package priority

import "scrumboy/internal/store"

var _ MutationStore = (*store.Store)(nil)
var _ PriorityReadStore = (*store.Store)(nil)
