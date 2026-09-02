package cache

import (
	"reflect"
	"testing"
)

func testCacheIdentity() CacheIdentity {
	return CacheIdentity{ProtocolVersion: PrefixProtocolVersion, ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock-tokenizer", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1", BlockSizeTokens: 4, CacheFormatVersion: "mock-kv-v1", KVLayout: "test-fp16"}
}

func TestPrefixHashGolden(t *testing.T) {
	blocks, err := BuildTokenBlocks([]TokenID{1, 2, 3, 4, 5, 6, 7, 8, 9}, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := BuildPrefixChain(testCacheIdentity(), blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("partial tail entered chain: %+v", chain)
	}
	want := []string{"c566c7ffa7b1bcb5641559e694fcf6bc6f43f65e6664fcee21c21d38bbe29edf", "dc56711603fa50501743eced923f2ab21038d57dd67e7182424463fce600571d"}
	for i, item := range chain {
		if item.PrefixHash.String() != want[i] {
			t.Fatalf("hash %d changed: got=%s want=%s", i, item.PrefixHash.String(), want[i])
		}
		if i > 0 && item.ParentHash != chain[i-1].PrefixHash {
			t.Fatalf("parent mismatch at %d", i)
		}
	}
}

func TestPrefixHashIdentityAndTokenIsolation(t *testing.T) {
	tokens := []TokenID{1, 2, 3, 4, 5, 6, 7, 8}
	blocks, _ := BuildTokenBlocks(tokens, 4, 100)
	base, _ := BuildPrefixChain(testCacheIdentity(), blocks)
	changedTokens := append([]TokenID(nil), tokens...)
	changedTokens[2]++
	changedBlocks, _ := BuildTokenBlocks(changedTokens, 4, 100)
	changed, _ := BuildPrefixChain(testCacheIdentity(), changedBlocks)
	if base[0].PrefixHash == changed[0].PrefixHash || base[1].PrefixHash == changed[1].PrefixHash {
		t.Fatal("token change did not affect current and descendant hashes")
	}
	identities := []CacheIdentity{testCacheIdentity(), testCacheIdentity(), testCacheIdentity(), testCacheIdentity(), testCacheIdentity(), testCacheIdentity()}
	identities[0].ModelRevision = "v2"
	identities[1].TokenizerRevision = "v2"
	identities[2].BlockSizeTokens = 2
	identities[3].ChatTemplateVersion = "chat-v2"
	identities[4].CacheFormatVersion = "mock-kv-v2"
	identities[5].KVLayout = "bf16"
	for _, identity := range identities {
		candidateBlocks := blocks
		if identity.BlockSizeTokens != 4 {
			candidateBlocks, _ = BuildTokenBlocks(tokens, identity.BlockSizeTokens, 100)
		}
		candidate, _ := BuildPrefixChain(identity, candidateBlocks)
		if len(candidate) > 0 && candidate[0].PrefixHash == base[0].PrefixHash {
			t.Fatalf("identity did not isolate hash: %+v", identity)
		}
	}
	again, _ := BuildPrefixChain(testCacheIdentity(), blocks)
	if !reflect.DeepEqual(base, again) {
		t.Fatal("prefix hashing is not deterministic")
	}
}

func TestPrefixHashUnicodeTokensStable(t *testing.T) {
	tokens := []TokenID{20320, 22909, 44, 99, 97, 99, 104, 101}
	blocks, err := BuildTokenBlocks(tokens, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildPrefixChain(testCacheIdentity(), blocks)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPrefixChain(testCacheIdentity(), blocks)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 2 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}
