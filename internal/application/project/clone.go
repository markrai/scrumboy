package project

import (
	"time"

	"scrumboy/internal/store"
)

func cloneWorkflowColumns(columns []store.WorkflowColumn) []store.WorkflowColumn {
	if columns == nil {
		return nil
	}
	return append([]store.WorkflowColumn{}, columns...)
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneProject(value store.Project) store.Project {
	value.Image = cloneString(value.Image)
	value.OwnerUserID = cloneInt64(value.OwnerUserID)
	value.CreatorUserID = cloneInt64(value.CreatorUserID)
	value.ExpiresAt = cloneTime(value.ExpiresAt)
	return value
}

func cloneProjectContext(value store.ProjectContext) store.ProjectContext {
	value.Project = cloneProject(value.Project)
	return value
}

func cloneDeletedProjectSnapshot(value store.DeletedProjectSnapshot) store.DeletedProjectSnapshot {
	if value.MemberUserIDs != nil {
		value.MemberUserIDs = append([]int64{}, value.MemberUserIDs...)
	}
	return value
}
