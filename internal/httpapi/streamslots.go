package httpapi

import (
	"errors"
	"sync"
)

var errStreamLimit = errors.New("stream concurrency limit reached")

type streamSlots struct {
	mu         sync.Mutex
	global     int
	perIP      int
	perProject int
	active     int
	byIP       map[string]int
	byProject  map[int64]int
}

func newStreamSlots(global, perIP, perProject int) *streamSlots {
	if global <= 0 {
		global = 500
	}
	if perIP <= 0 {
		perIP = 10
	}
	if perProject <= 0 {
		perProject = 50
	}
	return &streamSlots{
		global:     global,
		perIP:      perIP,
		perProject: perProject,
		byIP:       make(map[string]int),
		byProject:  make(map[int64]int),
	}
}

func (s *streamSlots) acquire(projectID int64, ip string) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	if ip == "" {
		ip = "unknown"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= s.global {
		return nil, errStreamLimit
	}
	if s.byIP[ip] >= s.perIP {
		return nil, errStreamLimit
	}
	if s.byProject[projectID] >= s.perProject {
		return nil, errStreamLimit
	}
	s.active++
	s.byIP[ip]++
	s.byProject[projectID]++
	released := false
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if released {
			return
		}
		released = true
		s.active--
		s.byIP[ip]--
		if s.byIP[ip] <= 0 {
			delete(s.byIP, ip)
		}
		s.byProject[projectID]--
		if s.byProject[projectID] <= 0 {
			delete(s.byProject, projectID)
		}
	}, nil
}
