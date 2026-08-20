package cache

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func testTokenizer() *DeterministicMockTokenizer {
	return &DeterministicMockTokenizer{TokenizerID: TokenizerIdentity{ID: "mock-tokenizer", Revision: "v1"}, MaxInputBytes: 4096, MaxTokens: 1024}
}

func TestMockTokenizerDeterministicAndNonASCII(t *testing.T) {
	tokenizer := testTokenizer()
	input := "Hello, 世界! cache_1"
	first, err := tokenizer.Encode(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tokenizer.Encode(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("first=%v second=%v", first, second)
	}
	want := []TokenID{1411900134, 1688288556, 316165388, 3417169659, 1196566753}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("tokens changed: got=%v want=%v", first, want)
	}
}

func TestMockTokenizerEmptyCancellationAndLimit(t *testing.T) {
	tokenizer := testTokenizer()
	tokens, err := tokenizer.Encode(context.Background(), "")
	if err != nil || len(tokens) != 0 {
		t.Fatalf("tokens=%v err=%v", tokens, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tokenizer.Encode(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	tokenizer.MaxInputBytes = 2
	if _, err := tokenizer.Encode(context.Background(), "long"); !errors.Is(err, ErrTokenizerInputTooLarge) {
		t.Fatalf("got %v", err)
	}
}
