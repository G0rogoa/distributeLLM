package cache_test

import (
	"testing"
	"time"

	"distserve/internal/cache"
)

func TestAffinityIndexRecordsShadowWithTTL(t *testing.T) {
	now := time.Unix(100, 0)
	index := cache.NewAffinityIndex(time.Second)
	index.SetNowForTest(func() time.Time { return now })
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: 2, CacheFormatVersion: "kv"}
	blocks, err := cache.BuildTokenBlocks([]cache.TokenID{1, 2, 3, 4}, 2, 1024)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := cache.BuildPrefixChain(identity, blocks)
	if err != nil {
		t.Fatal(err)
	}
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	index.RecordShadow(worker, identity, chain, 4)
	match := index.Match(worker, identity, chain, 4)
	if match.Evidence != cache.EvidenceShadowEstimated || match.MatchedTokens != 4 || match.MatchedBlocks != 2 {
		t.Fatalf("match=%+v", match)
	}
	now = now.Add(2 * time.Second)
	if match := index.Match(worker, identity, chain, 4); match.Evidence != cache.EvidenceUnknown || match.MatchedTokens != 0 {
		t.Fatalf("expired match=%+v", match)
	}
}

func TestAffinityIndexClearsWorkerInstance(t *testing.T) {
	index := cache.NewAffinityIndex(time.Minute)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: 1, CacheFormatVersion: "kv"}
	blocks, _ := cache.BuildTokenBlocks([]cache.TokenID{1}, 1, 1024)
	chain, _ := cache.BuildPrefixChain(identity, blocks)
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	index.RecordShadow(worker, identity, chain, 1)
	index.ClearWorker(worker)
	if match := index.Match(worker, identity, chain, 1); match.Evidence != cache.EvidenceUnknown {
		t.Fatalf("match after clear=%+v", match)
	}
}
