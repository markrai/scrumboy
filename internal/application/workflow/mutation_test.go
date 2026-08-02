package workflow

import "scrumboy/internal/store"

var _ MutationStore = (*store.Store)(nil)
var _ WorkflowReadStore = (*store.Store)(nil)
