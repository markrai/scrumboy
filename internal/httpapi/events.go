package httpapi

import "encoding/json"

type refreshNeededEvent struct {
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
	Reason    string `json:"reason,omitempty"`
}

type membersUpdatedEvent struct {
	Type      string `json:"type"`
	ProjectID int64  `json:"projectId"`
}

func (s *Server) emitRefreshNeeded(projectID int64, reason string) {
	if s.sink == nil {
		return
	}
	payload, err := json.Marshal(refreshNeededEvent{
		Type:      "refresh_needed",
		ProjectID: projectID,
		Reason:    reason,
	})
	if err != nil {
		return
	}
	s.sink.Emit(projectID, payload)
}

func (s *Server) emitMembersUpdated(projectID int64) {
	if s.sink == nil {
		return
	}
	payload, err := json.Marshal(membersUpdatedEvent{
		Type:      "members_updated",
		ProjectID: projectID,
	})
	if err != nil {
		return
	}
	s.sink.Emit(projectID, payload)
}
