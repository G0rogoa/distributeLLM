package cache

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidIdentity = errors.New("invalid cache identity")
	ErrPromptTooLarge  = errors.New("prompt exceeds size limit")
)

type PromptIdentity struct {
	ModelID             string
	ModelRevision       string
	TokenizerID         string
	TokenizerRevision   string
	ChatTemplateVersion string
	AdapterID           string
}

type PromptMessage struct {
	Role    string
	Content string
}

type BuiltPrompt struct {
	Identity PromptIdentity
	Text     string
}

type PromptBuilder struct {
	Identity PromptIdentity
	MaxBytes int
}

func (b PromptBuilder) Build(ctx context.Context, messages []PromptMessage) (BuiltPrompt, error) {
	if err := ctx.Err(); err != nil {
		return BuiltPrompt{}, err
	}
	if err := validatePromptIdentity(b.Identity); err != nil {
		return BuiltPrompt{}, err
	}
	if b.MaxBytes < 1 {
		return BuiltPrompt{}, fmt.Errorf("max prompt bytes must be positive: %w", ErrPromptTooLarge)
	}
	var text strings.Builder
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return BuiltPrompt{}, err
		}
		// Lengths make the template unambiguous even when content contains markers.
		line := fmt.Sprintf("<|message|>\nrole-bytes:%d\n%s\ncontent-bytes:%d\n%s\n<|end|>\n", len(message.Role), message.Role, len(message.Content), message.Content)
		if text.Len()+len(line) > b.MaxBytes {
			return BuiltPrompt{}, ErrPromptTooLarge
		}
		text.WriteString(line)
	}
	return BuiltPrompt{Identity: b.Identity, Text: text.String()}, nil
}

func validatePromptIdentity(identity PromptIdentity) error {
	if identity.ModelID == "" || identity.ModelRevision == "" || identity.TokenizerID == "" || identity.TokenizerRevision == "" || identity.ChatTemplateVersion == "" {
		return ErrInvalidIdentity
	}
	return nil
}
