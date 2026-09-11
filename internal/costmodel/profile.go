package costmodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"distserve/internal/cache"
)

var (
	ErrInvalidProfile   = errors.New("invalid cost profile")
	ErrIdentityMismatch = errors.New("cost profile identity mismatch")
)

type Source string

const (
	SourceCalibrated Source = "calibrated"
	SourceDefault    Source = "default"
	SourceFallback   Source = "fallback"
)

type Profile struct {
	Version                 int       `json:"version"`
	ModelIdentityHash       string    `json:"model_identity_hash"`
	HardwareClass           string    `json:"hardware_class"`
	PrefillFixedMS          float64   `json:"prefill_fixed_ms"`
	PrefillMSPerToken       float64   `json:"prefill_ms_per_token"`
	DecodeFixedMS           float64   `json:"decode_fixed_ms"`
	DecodeMSPerToken        float64   `json:"decode_ms_per_token"`
	RunningRequestMS        float64   `json:"running_request_ms"`
	WaitingRequestMS        float64   `json:"waiting_request_ms"`
	ReservationMS           float64   `json:"reservation_ms"`
	ShadowConfidence        float64   `json:"shadow_confidence"`
	SampleCount             int       `json:"sample_count"`
	PrefillSampleCount      int       `json:"prefill_sample_count"`
	DecodeSampleCount       int       `json:"decode_sample_count"`
	PrefillRSquared         float64   `json:"prefill_r_squared"`
	DecodeRSquared          float64   `json:"decode_r_squared"`
	PrefillInterceptClipped bool      `json:"prefill_intercept_clipped"`
	DecodeInterceptClipped  bool      `json:"decode_intercept_clipped"`
	PrefillSource           Source    `json:"prefill_source"`
	DecodeSource            Source    `json:"decode_source"`
	QueueSource             Source    `json:"queue_source"`
	CalibratedAt            time.Time `json:"calibrated_at"`
	ValidTokenRange         [2]int    `json:"valid_token_range"`
	Source                  Source    `json:"source"`
}

type Summary struct {
	Version           int     `json:"version"`
	HardwareClass     string  `json:"hardware_class"`
	Source            Source  `json:"source"`
	PrefillMSPerToken float64 `json:"prefill_ms_per_token"`
	DecodeMSPerToken  float64 `json:"decode_ms_per_token"`
	ShadowConfidence  float64 `json:"shadow_confidence"`
	SampleCount       int     `json:"sample_count"`
	ValidTokenRange   [2]int  `json:"valid_token_range"`
	PrefillSource     Source  `json:"prefill_source"`
	DecodeSource      Source  `json:"decode_source"`
	QueueSource       Source  `json:"queue_source"`
}

func DefaultProfile() Profile {
	return Profile{
		Version:           1,
		HardwareClass:     "unknown",
		PrefillMSPerToken: 0.5,
		DecodeMSPerToken:  1,
		RunningRequestMS:  20,
		WaitingRequestMS:  40,
		ReservationMS:     20,
		ShadowConfidence:  0.5,
		ValidTokenRange:   [2]int{1, 262144},
		Source:            SourceDefault,
		PrefillSource:     SourceDefault,
		DecodeSource:      SourceDefault,
		QueueSource:       SourceDefault,
	}
}

func LoadFile(path string, identity cache.CacheIdentity, allowMismatch bool) (Profile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return Profile{}, fmt.Errorf("decode cost profile: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, err
	}
	if profile.ModelIdentityHash != "" {
		hash, err := identity.Hash()
		if err != nil {
			return Profile{}, err
		}
		if profile.ModelIdentityHash != hash.String() && !allowMismatch {
			return Profile{}, ErrIdentityMismatch
		}
	}
	if profile.Source == "" {
		profile.Source = SourceCalibrated
	}
	return profile, nil
}

func (p Profile) Validate() error {
	if p.Version != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidProfile, p.Version)
	}
	if p.PrefillFixedMS < 0 || p.PrefillMSPerToken < 0 || p.DecodeFixedMS < 0 || p.DecodeMSPerToken < 0 || p.RunningRequestMS < 0 || p.WaitingRequestMS < 0 || p.ReservationMS < 0 {
		return fmt.Errorf("%w: negative timing value", ErrInvalidProfile)
	}
	if p.ShadowConfidence < 0 || p.ShadowConfidence > 1 {
		return fmt.Errorf("%w: shadow confidence must be in [0,1]", ErrInvalidProfile)
	}
	if p.ValidTokenRange[0] < 0 || p.ValidTokenRange[1] < p.ValidTokenRange[0] {
		return fmt.Errorf("%w: invalid token range", ErrInvalidProfile)
	}
	return nil
}

func (p Profile) Summary() Summary {
	return Summary{Version: p.Version, HardwareClass: p.HardwareClass, Source: p.Source, PrefillMSPerToken: p.PrefillMSPerToken, DecodeMSPerToken: p.DecodeMSPerToken, ShadowConfidence: p.ShadowConfidence, SampleCount: p.SampleCount, ValidTokenRange: p.ValidTokenRange, PrefillSource: p.PrefillSource, DecodeSource: p.DecodeSource, QueueSource: p.QueueSource}
}
