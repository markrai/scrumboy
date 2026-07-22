package oidc

import (
	"crypto/sha256"
	"sync"
	"time"
)

type FlowPurpose string

const (
	FlowLogin       FlowPurpose = "login"
	FlowSetPassword FlowPurpose = "set_password"
	FlowLink        FlowPurpose = "link"
)

type loginState struct {
	Purpose          FlowPurpose
	Nonce            string
	PKCEVerifier     string
	ReturnTo         string
	UserID           int64
	SessionTokenHash [32]byte
	CreatedAt        time.Time
}

func (s *loginState) sensitive() bool { return s.Purpose != "" && s.Purpose != FlowLogin }

type stateStore struct {
	mu           sync.Mutex
	loginTTL     time.Duration
	sensitiveTTL time.Duration
	m            map[[32]byte]*loginState
}

func newStateStore(loginTTL time.Duration) *stateStore {
	return &stateStore{loginTTL: loginTTL, sensitiveTTL: 5 * time.Minute, m: make(map[[32]byte]*loginState)}
}

func stateHash(raw string) [32]byte { return sha256.Sum256([]byte(raw)) }

func sessionHash(raw string) [32]byte { return sha256.Sum256([]byte(raw)) }

func (s *stateStore) Put(raw string, ls *loginState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked()
	s.m[stateHash(raw)] = ls
}

func (s *stateStore) Take(raw string) *loginState {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := stateHash(raw)
	ls, ok := s.m[key]
	if !ok {
		return nil
	}
	delete(s.m, key)
	ttl := s.loginTTL
	if ls.sensitive() {
		ttl = s.sensitiveTTL
	}
	if time.Since(ls.CreatedAt) > ttl {
		return nil
	}
	return ls
}

func (s *stateStore) evictLocked() {
	now := time.Now()
	for k, v := range s.m {
		ttl := s.loginTTL
		if v.sensitive() {
			ttl = s.sensitiveTTL
		}
		if now.Sub(v.CreatedAt) > ttl {
			delete(s.m, k)
		}
	}
}
