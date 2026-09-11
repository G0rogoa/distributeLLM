package costmodel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"distserve/internal/cache"
)

func testIdentity() cache.CacheIdentity {
	return cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "tok", TokenizerRevision: "tr", ChatTemplateVersion: "chat", BlockSizeTokens: 16, CacheFormatVersion: "kv", KVLayout: "layout"}
}

func TestProfileLoadValidatesIdentity(t *testing.T) {
	identity := testIdentity()
	hash, err := identity.Hash()
	if err != nil {
		t.Fatal(err)
	}
	profile := DefaultProfile()
	profile.ModelIdentityHash = hash.String()
	profile.Source = SourceCalibrated
	raw, _ := json.Marshal(profile)
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path, identity, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelIdentityHash != hash.String() || got.Source != SourceCalibrated {
		t.Fatalf("profile=%+v", got)
	}
	other := identity
	other.ModelRevision = "other"
	if _, err := LoadFile(path, other, false); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestProfileRejectsInvalidValues(t *testing.T) {
	profile := DefaultProfile()
	profile.ShadowConfidence = 1.5
	if err := profile.Validate(); err == nil {
		t.Fatal("invalid profile accepted")
	}
}

func TestValidPrefillTokenRangeUsesOnlyValidPrefillSamples(t *testing.T) {
	samples := []CalibrationSample{
		{Kind: "prefill", InputTokens: 8192, TTFTMS: 10, Valid: true},
		{Kind: "prefill", InputTokens: 256, TTFTMS: 2, Valid: true},
		{Kind: "prefill", InputTokens: 1, TTFTMS: 0, Valid: true},
		{Kind: "decode", InputTokens: 16384, TTFTMS: 1, Valid: true},
	}
	if got := ValidPrefillTokenRange(samples); got != [2]int{256, 8192} {
		t.Fatalf("range=%v", got)
	}
}

func TestOnlineStoreRejectsBadSamplesAndBoundsDecode(t *testing.T) {
	store := NewOnlineStore(OnlineConfig{Enabled: true, Alpha: 0.5, MinDecodeMSPerTok: 0.1, MaxDecodeMSPerTok: 10, MaxServiceMS: 1000})
	if store.Add(Observation{WorkerID: "w", InstanceID: "i1", ServiceMS: 2000}) {
		t.Fatal("outlier accepted")
	}
	if !store.Add(Observation{WorkerID: "w", InstanceID: "i1", TTFTMS: 10, ServiceMS: 100, CompletionTokens: 10, UsageValid: true, DecodeObservationMS: 20, DecodeObservationOK: true, CompletedAt: time.Now()}) {
		t.Fatal("sample rejected")
	}
	if !store.Add(Observation{WorkerID: "w", InstanceID: "i1", TTFTMS: 20, ServiceMS: 120, CompletionTokens: 1, UsageValid: true, DecodeObservationMS: 500, DecodeObservationOK: true, CompletedAt: time.Now()}) {
		t.Fatal("bounded sample rejected")
	}
	got := store.Snapshot()["w/i1"]
	if got.SampleCount != 2 || got.ObservedDecodeMSPerToken != 2 {
		t.Fatalf("estimate=%+v", got)
	}
	if !store.Add(Observation{WorkerID: "w", InstanceID: "i2", TTFTMS: 30, ServiceMS: 40, CompletedAt: time.Now()}) {
		t.Fatal("replacement sample rejected")
	}
	if got := store.Snapshot()["w/i2"]; got.SampleCount != 1 || got.ObservedTTFTMS != 30 {
		t.Fatalf("replacement inherited estimate: %+v", got)
	}
}
