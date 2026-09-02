package gateway

import (
	"sync"
	"time"

	"distserve/internal/scheduler"
)

type DecisionRecord struct {
	RequestID   string                     `json:"request_id"`
	DecidedAt   time.Time                  `json:"decided_at"`
	Strategy    string                     `json:"strategy"`
	WorkerID    string                     `json:"worker_id"`
	InstanceID  string                     `json:"instance_id"`
	Score       float64                    `json:"score"`
	Reason      string                     `json:"reason"`
	Candidates  []scheduler.CandidateScore `json:"candidates"`
	SnapshotAge string                     `json:"snapshot_age"`
}

type decisionStore struct {
	mu    sync.Mutex
	limit int
	items []DecisionRecord
}

func newDecisionStore(limit int) *decisionStore {
	if limit < 1 {
		limit = 1
	}
	return &decisionStore{limit: limit}
}

func (s *decisionStore) Add(record DecisionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) == s.limit {
		copy(s.items, s.items[1:])
		s.items[len(s.items)-1] = record
		return
	}
	s.items = append(s.items, record)
}

func (s *decisionStore) Snapshot() []DecisionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DecisionRecord, len(s.items))
	copy(out, s.items)
	return out
}
