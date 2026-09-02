package lifecycle

import (
	"sync"
	"time"
)

type Request struct {
	RequestID            string    `json:"request_id"`
	ReceivedAt           time.Time `json:"received_at"`
	AdmittedAt           time.Time `json:"admitted_at,omitempty"`
	ScheduledAt          time.Time `json:"scheduled_at,omitempty"`
	ForwardedAt          time.Time `json:"forwarded_at,omitempty"`
	FirstTokenAt         time.Time `json:"first_token_at,omitempty"`
	CompletedAt          time.Time `json:"completed_at,omitempty"`
	FailedAt             time.Time `json:"failed_at,omitempty"`
	SelectedWorker       string    `json:"selected_worker,omitempty"`
	SelectedInstance     string    `json:"selected_instance,omitempty"`
	BackendType          string    `json:"backend_type,omitempty"`
	SchedulerStrategy    string    `json:"scheduler_strategy,omitempty"`
	RetryCount           int       `json:"retry_count"`
	InputTokens          int       `json:"input_tokens"`
	OutputTokens         int       `json:"output_tokens"`
	ResponseStarted      bool      `json:"response_started"`
	FinalStatus          string    `json:"final_status"`
	CacheFullBlocks      int       `json:"cache_full_blocks"`
	CachePredictedBlocks int       `json:"cache_predicted_blocks"`
	CachePredictedTokens int       `json:"cache_predicted_tokens"`
	CacheEvidence        string    `json:"cache_evidence,omitempty"`
	ShadowAffinityMatch  bool      `json:"shadow_affinity_match"`
	CacheActualBlocks    int       `json:"cache_actual_blocks"`
	CacheActualTokens    int       `json:"cache_actual_tokens"`
	CacheViewState       string    `json:"cache_view_state,omitempty"`
	CacheFillReserved    bool      `json:"cache_fill_reserved"`
	CachePredictionMiss  bool      `json:"cache_prediction_miss"`
}

type Store struct {
	mu    sync.Mutex
	items []Request
	next  int
	full  bool
}

func New(capacity int) *Store {
	if capacity < 1 {
		capacity = 1
	}
	return &Store{items: make([]Request, capacity)}
}

func (s *Store) Add(request Request) {
	s.mu.Lock()
	s.items[s.next] = request
	s.next = (s.next + 1) % len(s.items)
	if s.next == 0 {
		s.full = true
	}
	s.mu.Unlock()
}

func (s *Store) Snapshot() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.next
	if s.full {
		count = len(s.items)
	}
	result := make([]Request, 0, count)
	if s.full {
		result = append(result, s.items[s.next:]...)
	}
	result = append(result, s.items[:s.next]...)
	return result
}

func (s *Store) Find(requestID string) (Request, bool) {
	for _, request := range s.Snapshot() {
		if request.RequestID == requestID {
			return request, true
		}
	}
	return Request{}, false
}
