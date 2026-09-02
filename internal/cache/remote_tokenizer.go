package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type tokenizerRequest struct {
	Text             string         `json:"text"`
	ExpectedIdentity PromptIdentity `json:"expected_identity"`
}

type tokenizerResponse struct {
	TokenIDs   []TokenID      `json:"token_ids"`
	TokenCount int            `json:"token_count"`
	Identity   PromptIdentity `json:"identity"`
}

type tokenizerErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (t RemoteTokenizer) Encode(ctx context.Context, text string) ([]TokenID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(t.URL) == "" {
		return nil, ErrTokenizerUnavailable
	}
	if err := validatePromptIdentity(t.ExpectedIdentity); err != nil {
		return nil, fmt.Errorf("remote tokenizer expected identity: %w", err)
	}
	if t.TokenizerID.ID == "" || t.TokenizerID.Revision == "" {
		return nil, fmt.Errorf("tokenizer ID and revision are required: %w", ErrInvalidIdentity)
	}
	requestCtx := ctx
	cancel := func() {}
	if t.Timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, t.Timeout)
	}
	defer cancel()
	body, err := json.Marshal(tokenizerRequest{Text: text, ExpectedIdentity: t.ExpectedIdentity})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(t.URL, "/")+"/v1/tokenize", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: timeout", ErrTokenizerUnavailable)
		}
		return nil, fmt.Errorf("%w: %v", ErrTokenizerUnavailable, err)
	}
	defer response.Body.Close()
	limit := t.MaxResponseBytes
	if limit < 1 {
		limit = 4 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read response: %v", ErrTokenizerUnavailable, err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: response too large", ErrTokenizerUnavailable)
	}
	if response.StatusCode != http.StatusOK {
		var errorBody tokenizerErrorResponse
		if json.Unmarshal(raw, &errorBody) == nil && errorBody.Error.Code != "" {
			return nil, fmt.Errorf("%w: %s", ErrTokenizerUnavailable, errorBody.Error.Code)
		}
		return nil, fmt.Errorf("%w: status %s", ErrTokenizerUnavailable, response.Status)
	}
	var decoded tokenizerResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid response: %v", ErrTokenizerUnavailable, err)
	}
	if decoded.TokenCount != len(decoded.TokenIDs) {
		return nil, fmt.Errorf("%w: token_count mismatch", ErrTokenizerUnavailable)
	}
	if t.MaxTokens > 0 && len(decoded.TokenIDs) > t.MaxTokens {
		return nil, ErrTokenizerInputTooLarge
	}
	if decoded.Identity != t.ExpectedIdentity {
		return nil, ErrTokenizerIdentity
	}
	return append([]TokenID(nil), decoded.TokenIDs...), nil
}
