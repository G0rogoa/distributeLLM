package costmodel

import (
	"strings"
	"sync"
	"time"
)

type OnlineConfig struct {
	Enabled           bool
	Alpha             float64
	MinSamples        int
	MinDecodeMSPerTok float64
	MaxDecodeMSPerTok float64
	MaxServiceMS      float64
}

type Observation struct {
	WorkerID            string
	InstanceID          string
	TTFTMS              float64
	ServiceMS           float64
	CompletionTokens    int
	UsageValid          bool
	DecodeObservationMS float64
	DecodeObservationOK bool
	CompletedAt         time.Time
}

type OnlineEstimate struct {
	ObservedTTFTMS           float64   `json:"observed_ttft_ms"`
	ObservedServiceMS        float64   `json:"observed_service_ms"`
	ObservedDecodeMSPerToken float64   `json:"observed_decode_ms_per_token"`
	SampleCount              int       `json:"sample_count"`
	LastUpdated              time.Time `json:"last_updated"`
}

type OnlineStore struct {
	mu     sync.Mutex
	config OnlineConfig
	items  map[string]OnlineEstimate
}

func NewOnlineStore(config OnlineConfig) *OnlineStore {
	if config.Alpha <= 0 || config.Alpha > 1 {
		config.Alpha = 0.2
	}
	if config.MinDecodeMSPerTok <= 0 {
		config.MinDecodeMSPerTok = 0.01
	}
	if config.MaxDecodeMSPerTok <= 0 {
		config.MaxDecodeMSPerTok = 1000
	}
	if config.MaxServiceMS <= 0 {
		config.MaxServiceMS = 10 * 60 * 1000
	}
	return &OnlineStore{config: config, items: map[string]OnlineEstimate{}}
}

func (s *OnlineStore) Add(obs Observation) bool {
	if s == nil || !s.config.Enabled || obs.WorkerID == "" || obs.InstanceID == "" || obs.ServiceMS <= 0 || obs.ServiceMS > s.config.MaxServiceMS {
		return false
	}
	if obs.CompletedAt.IsZero() {
		obs.CompletedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.Join([]string{obs.WorkerID, obs.InstanceID}, "/")
	current := s.items[key]
	current.ObservedTTFTMS = ewma(current.ObservedTTFTMS, obs.TTFTMS, current.SampleCount, s.config.Alpha)
	current.ObservedServiceMS = ewma(current.ObservedServiceMS, obs.ServiceMS, current.SampleCount, s.config.Alpha)
	if obs.UsageValid && obs.CompletionTokens > 0 && obs.DecodeObservationOK {
		perToken := obs.DecodeObservationMS / float64(obs.CompletionTokens)
		if perToken >= s.config.MinDecodeMSPerTok && perToken <= s.config.MaxDecodeMSPerTok {
			current.ObservedDecodeMSPerToken = ewma(current.ObservedDecodeMSPerToken, perToken, current.SampleCount, s.config.Alpha)
		}
	}
	current.SampleCount++
	current.LastUpdated = obs.CompletedAt
	s.items[key] = current
	return true
}

func (s *OnlineStore) Snapshot() map[string]OnlineEstimate {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make(map[string]OnlineEstimate, len(s.items))
	for key, value := range s.items {
		result[key] = value
	}
	return result
}

func ewma(current, next float64, samples int, alpha float64) float64 {
	if next <= 0 {
		return current
	}
	if samples == 0 || current == 0 {
		return next
	}
	return alpha*next + (1-alpha)*current
}
