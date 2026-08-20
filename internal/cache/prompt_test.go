package cache

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func testPromptIdentity() PromptIdentity {
	return PromptIdentity{ModelID: "mock-llm", ModelRevision: "v1", TokenizerID: "mock-tokenizer", TokenizerRevision: "v1", ChatTemplateVersion: "chat-v1"}
}

func TestPromptBuilderDeterministicAndSensitiveToMessages(t *testing.T) {
	builder := PromptBuilder{Identity: testPromptIdentity(), MaxBytes: 4096}
	messages := []PromptMessage{{Role: "system", Content: "You are helpful."}, {Role: "user", Content: "Explain KV cache."}}
	first, err := builder.Build(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build(context.Background(), append([]PromptMessage(nil), messages...))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("prompt is not deterministic:\n%+v\n%+v", first, second)
	}
	changedSystem := append([]PromptMessage(nil), messages...)
	changedSystem[0].Content = "You are concise."
	changed, _ := builder.Build(context.Background(), changedSystem)
	if changed.Text == first.Text {
		t.Fatal("system message did not change prompt")
	}
	changedRole := append([]PromptMessage(nil), messages...)
	changedRole[1].Role = "assistant"
	changed, _ = builder.Build(context.Background(), changedRole)
	if changed.Text == first.Text {
		t.Fatal("message role did not change prompt")
	}
}

func TestPromptIdentityChangesWithoutSamplingParameters(t *testing.T) {
	identity := testPromptIdentity()
	builder := PromptBuilder{Identity: identity, MaxBytes: 1024}
	messages := []PromptMessage{{Role: "user", Content: "same input"}}
	first, err := builder.Build(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	identity.ChatTemplateVersion = "chat-v2"
	second, err := (PromptBuilder{Identity: identity, MaxBytes: 1024}).Build(context.Background(), messages)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != second.Text {
		t.Fatal("template identity version should not silently mutate current template text")
	}
	if first.Identity == second.Identity {
		t.Fatal("template version did not change prompt identity")
	}
	// Stream, temperature, and max_tokens are deliberately absent from PromptBuilder input.
}

func TestPromptBuilderLimitsAndCancellation(t *testing.T) {
	builder := PromptBuilder{Identity: testPromptIdentity(), MaxBytes: 32}
	if _, err := builder.Build(context.Background(), []PromptMessage{{Role: "user", Content: strings.Repeat("x", 64)}}); !errors.Is(err, ErrPromptTooLarge) {
		t.Fatalf("got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := builder.Build(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
