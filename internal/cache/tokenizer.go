package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode"
)

type TokenID uint32
type TokenizerMode string

const (
	TokenizerModeMock     TokenizerMode = "mock"
	TokenizerModeRemote   TokenizerMode = "remote"
	TokenizerModeDisabled TokenizerMode = "disabled"
)

type TokenizerIdentity struct {
	ID       string
	Revision string
}

type Tokenizer interface {
	Identity() TokenizerIdentity
	Encode(context.Context, string) ([]TokenID, error)
}

var (
	ErrTokenizerInputTooLarge = errors.New("tokenizer input exceeds size limit")
	ErrTokenizerDisabled      = errors.New("tokenizer disabled")
	ErrTokenizerUnavailable   = errors.New("tokenizer unavailable")
)

type DisabledTokenizer struct{}

func (DisabledTokenizer) Identity() TokenizerIdentity {
	return TokenizerIdentity{ID: "disabled", Revision: "none"}
}
func (DisabledTokenizer) Encode(context.Context, string) ([]TokenID, error) {
	return nil, ErrTokenizerDisabled
}

type RemoteTokenizer struct {
	TokenizerID TokenizerIdentity
	URL         string
}

func (t RemoteTokenizer) Identity() TokenizerIdentity { return t.TokenizerID }
func (t RemoteTokenizer) Encode(context.Context, string) ([]TokenID, error) {
	if t.URL == "" {
		return nil, ErrTokenizerUnavailable
	}
	return nil, ErrTokenizerUnavailable
}

type DeterministicMockTokenizer struct {
	TokenizerID   TokenizerIdentity
	MaxInputBytes int
	MaxTokens     int
}

func (t *DeterministicMockTokenizer) Identity() TokenizerIdentity { return t.TokenizerID }

func (t *DeterministicMockTokenizer) Encode(ctx context.Context, text string) ([]TokenID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.TokenizerID.ID == "" || t.TokenizerID.Revision == "" {
		return nil, fmt.Errorf("tokenizer ID and revision are required: %w", ErrInvalidIdentity)
	}
	if t.MaxInputBytes < 1 || t.MaxTokens < 1 || len(text) > t.MaxInputBytes {
		return nil, ErrTokenizerInputTooLarge
	}
	parts := splitStable(text)
	if len(parts) > t.MaxTokens {
		return nil, ErrTokenizerInputTooLarge
	}
	result := make([]TokenID, 0, len(parts))
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte("distserve-mock-token-v1\x00" + part))
		result = append(result, TokenID(binary.BigEndian.Uint32(hash[:4])))
	}
	return result, nil
}

func splitStable(text string) []string {
	parts := make([]string, 0)
	runes := []rune(text)
	for index := 0; index < len(runes); {
		if unicode.IsSpace(runes[index]) {
			index++
			continue
		}
		start := index
		if unicode.IsLetter(runes[index]) || unicode.IsDigit(runes[index]) || runes[index] == '_' {
			index++
			for index < len(runes) && (unicode.IsLetter(runes[index]) || unicode.IsDigit(runes[index]) || runes[index] == '_') {
				index++
			}
		} else {
			index++
		}
		parts = append(parts, string(runes[start:index]))
	}
	return parts
}
