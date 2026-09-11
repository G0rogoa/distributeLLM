package cache_test

import (
	"sync"
	"testing"
	"time"

	"distserve/internal/cache"
)

func TestAffinityIndexRecordsShadowWithTTL(t *testing.T) {
	now := time.Unix(100, 0)
	index := cache.NewAffinityIndex(time.Second)
	index.SetNowForTest(func() time.Time { return now })
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: 2, CacheFormatVersion: "kv", KVLayout: "test-fp16"}
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
	if stats := index.Stats(); stats.Hits != 1 || stats.Entries != 2 || stats.MatchedBlocks != 2 || stats.MatchedTokens != 4 {
		t.Fatalf("stats=%+v", stats)
	}
	now = now.Add(2 * time.Second)
	if match := index.Match(worker, identity, chain, 4); match.Evidence != cache.EvidenceUnknown || match.MatchedTokens != 0 {
		t.Fatalf("expired match=%+v", match)
	}
	if stats := index.Stats(); stats.Misses != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestAffinityIndexMatchesLongestCommonPrefix(t *testing.T) {
	index := cache.NewAffinityIndex(time.Minute)
	identity := testIdentity(2)
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	recorded := prefixChain(t, identity, []cache.TokenID{1, 2, 3, 4, 5, 6, 7})
	index.RecordShadow(worker, identity, recorded, 7)

	tests := []struct {
		name          string
		tokens        []cache.TokenID
		blocks        int
		matchedTokens int
	}{
		{name: "same full prompt", tokens: []cache.TokenID{1, 2, 3, 4, 5, 6, 7}, blocks: 3, matchedTokens: 6},
		{name: "different partial suffix", tokens: []cache.TokenID{1, 2, 3, 4, 5, 6, 8}, blocks: 3, matchedTokens: 6},
		{name: "different third block", tokens: []cache.TokenID{1, 2, 3, 4, 9, 10}, blocks: 2, matchedTokens: 4},
		{name: "no common prefix", tokens: []cache.TokenID{9, 10}, blocks: 0, matchedTokens: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain := prefixChain(t, identity, tt.tokens)
			match := index.Match(worker, identity, chain, len(tt.tokens))
			if match.MatchedBlocks != tt.blocks || match.MatchedTokens != tt.matchedTokens {
				t.Fatalf("match=%+v", match)
			}
			if tt.blocks == 0 && match.Evidence != cache.EvidenceUnknown {
				t.Fatalf("evidence=%s", match.Evidence)
			}
		})
	}
}

func TestAffinityIndexIdentityAndWorkerIsolation(t *testing.T) {
	index := cache.NewAffinityIndex(time.Minute)
	identity := testIdentity(2)
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	chain := prefixChain(t, identity, []cache.TokenID{1, 2, 3, 4})
	index.RecordShadow(worker, identity, chain, 4)

	otherIdentity := identity
	otherIdentity.ModelRevision = "other"
	otherChain := prefixChain(t, otherIdentity, []cache.TokenID{1, 2, 3, 4})
	if match := index.Match(worker, otherIdentity, otherChain, 4); match.Evidence != cache.EvidenceUnknown {
		t.Fatalf("identity leaked: %+v", match)
	}
	otherWorker := cache.WorkerInstanceKey{WorkerID: "w2", InstanceID: "i2"}
	if match := index.Match(otherWorker, identity, chain, 4); match.Evidence != cache.EvidenceUnknown {
		t.Fatalf("worker leaked: %+v", match)
	}
}

func testIdentity(blockSize int) cache.CacheIdentity {
	return cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: blockSize, CacheFormatVersion: "kv", KVLayout: "test-fp16"}
}

func prefixChain(t *testing.T, identity cache.CacheIdentity, tokens []cache.TokenID) []cache.PrefixBlock {
	t.Helper()
	blocks, err := cache.BuildTokenBlocks(tokens, identity.BlockSizeTokens, 1024)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := cache.BuildPrefixChain(identity, blocks)
	if err != nil {
		t.Fatal(err)
	}
	return chain
}

func TestAffinityIndexClearsWorkerInstance(t *testing.T) {
	index := cache.NewAffinityIndex(time.Minute)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: 1, CacheFormatVersion: "kv", KVLayout: "test-fp16"}
	blocks, _ := cache.BuildTokenBlocks([]cache.TokenID{1}, 1, 1024)
	chain, _ := cache.BuildPrefixChain(identity, blocks)
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	index.RecordShadow(worker, identity, chain, 1)
	index.ClearWorker(worker)
	if match := index.Match(worker, identity, chain, 1); match.Evidence != cache.EvidenceUnknown {
		t.Fatalf("match after clear=%+v", match)
	}
	if stats := index.Stats(); stats.ClearedOnInstanceChange != 1 || stats.Entries != 0 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestAffinityIndexEvictsWhenBounded(t *testing.T) {
	index := cache.NewBoundedAffinityIndex(time.Minute, 1)
	identity := cache.CacheIdentity{ProtocolVersion: cache.PrefixProtocolVersion, ModelID: "m", ModelRevision: "r", TokenizerID: "t", TokenizerRevision: "tr", ChatTemplateVersion: "ct", BlockSizeTokens: 1, CacheFormatVersion: "kv", KVLayout: "test-fp16"}
	blocks, _ := cache.BuildTokenBlocks([]cache.TokenID{1}, 1, 1024)
	first, _ := cache.BuildPrefixChain(identity, blocks)
	secondBlocks, _ := cache.BuildTokenBlocks([]cache.TokenID{2}, 1, 1024)
	second, _ := cache.BuildPrefixChain(identity, secondBlocks)
	index.RecordShadow(cache.WorkerInstanceKey{WorkerID: "w1", InstanceID: "i1"}, identity, first, 1)
	index.RecordShadow(cache.WorkerInstanceKey{WorkerID: "w2", InstanceID: "i2"}, identity, second, 1)
	if stats := index.Stats(); stats.Entries != 1 || stats.Evicted != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestAffinityIndexConcurrentRecordMatchAndClear(t *testing.T) {
	index := cache.NewAffinityIndex(time.Minute)
	identity := testIdentity(2)
	chain := prefixChain(t, identity, []cache.TokenID{1, 2, 3, 4})
	worker := cache.WorkerInstanceKey{WorkerID: "w", InstanceID: "i"}
	var wg sync.WaitGroup
	for goroutine := 0; goroutine < 8; goroutine++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 100; iteration++ {
				index.RecordShadow(worker, identity, chain, 4)
				index.Match(worker, identity, chain, 4)
				if iteration%10 == 0 {
					index.ClearWorker(worker)
				}
			}
		}()
	}
	wg.Wait()
}
