package cache

import (
	"context"
	"time"
)

type PrefixMatch struct {
	WorkerID          string         `json:"worker_id"`
	InstanceID        string         `json:"instance_id"`
	MatchedBlocks     int            `json:"matched_blocks"`
	MatchedTokens     int            `json:"matched_tokens"`
	TotalFullBlocks   int            `json:"total_full_blocks"`
	TotalInputTokens  int            `json:"total_input_tokens"`
	MatchRatio        float64        `json:"match_ratio"`
	EstimatedSavedMS  float64        `json:"estimated_saved_ms"`
	OldestLeaseExpiry time.Time      `json:"oldest_lease_expiry"`
	CacheViewState    CacheViewState `json:"cache_view_state"`
}

type RequestFeatures struct {
	Identity         CacheIdentity
	PrefixBlocks     []PrefixBlock
	TotalInputTokens int
	Matches          map[WorkerInstanceKey]PrefixMatch
	FillAffinity     map[WorkerInstanceKey]bool
}

type Runtime struct {
	Builder   PromptBuilder
	Tokenizer Tokenizer
	Identity  CacheIdentity
	Index     *CacheIndex
}

func (runtime *Runtime) Prepare(ctx context.Context, messages []PromptMessage) (*RequestFeatures, error) {
	built, err := runtime.Builder.Build(ctx, messages)
	if err != nil {
		return nil, err
	}
	tokens, err := runtime.Tokenizer.Encode(ctx, built.Text)
	if err != nil {
		return nil, err
	}
	blocks, err := BuildTokenBlocks(tokens, runtime.Identity.BlockSizeTokens, runtime.Builder.MaxBytes)
	if err != nil {
		return nil, err
	}
	chain, err := BuildPrefixChain(runtime.Identity, blocks)
	if err != nil {
		return nil, err
	}
	return &RequestFeatures{Identity: runtime.Identity, PrefixBlocks: chain, TotalInputTokens: len(tokens)}, nil
}

func (index *CacheIndex) Match(worker WorkerInstanceKey, identity CacheIdentity, blocks []PrefixBlock, totalTokens int, now time.Time) PrefixMatch {
	match := PrefixMatch{WorkerID: worker.WorkerID, InstanceID: worker.InstanceID, TotalFullBlocks: len(blocks), TotalInputTokens: totalTokens}
	identityHash, err := identity.Hash()
	if err != nil {
		return match
	}
	index.mu.RLock()
	defer index.mu.RUnlock()
	if index.instances[worker.WorkerID] != worker.InstanceID {
		return match
	}
	view := index.views[worker]
	if view == nil {
		return match
	}
	match.CacheViewState = view.state
	if now.Sub(view.lastUpdated) >= index.staleViewThreshold {
		match.CacheViewState = CacheViewStale
		return match
	}
	for _, block := range blocks {
		entry := index.byWorker[worker][CacheKey{IdentityHash: identityHash, PrefixHash: block.PrefixHash}]
		if entry == nil || !entry.LeaseExpires.After(now) || entry.ParentHash != block.ParentHash || entry.BlockIndex != block.Index {
			break
		}
		match.MatchedBlocks++
		match.MatchedTokens += block.TokenCount
		if match.OldestLeaseExpiry.IsZero() || entry.LeaseExpires.Before(match.OldestLeaseExpiry) {
			match.OldestLeaseExpiry = entry.LeaseExpires
		}
	}
	if totalTokens > 0 {
		match.MatchRatio = float64(match.MatchedTokens) / float64(totalTokens)
	}
	return match
}
