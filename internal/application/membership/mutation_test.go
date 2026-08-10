package membership

import "scrumboy/internal/store"

var _ MutationStore = (*store.Store)(nil)
var _ MemberListStore = (*store.Store)(nil)
