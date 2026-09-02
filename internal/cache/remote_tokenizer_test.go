package cache

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func remoteTestPromptIdentity() PromptIdentity {
	return PromptIdentity{ModelID: "model", ModelRevision: "rev", TokenizerID: "tok", TokenizerRevision: "tok-rev", ChatTemplateVersion: "chat-v1"}
}

func TestRemoteTokenizerEncodesAndChecksIdentity(t *testing.T) {
	identity := remoteTestPromptIdentity()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request tokenizerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Text == "" || request.ExpectedIdentity != identity {
			t.Fatalf("request=%+v", request)
		}
		writeTestJSON(w, tokenizerResponse{TokenIDs: []TokenID{1, 2, 3}, TokenCount: 3, Identity: identity})
	}))
	defer server.Close()
	tokenizer := RemoteTokenizer{TokenizerID: TokenizerIdentity{ID: "tok", Revision: "tok-rev"}, URL: server.URL, ExpectedIdentity: identity}
	tokens, err := tokenizer.Encode(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 3 || tokens[2] != 3 {
		t.Fatalf("tokens=%v", tokens)
	}
}

func TestRemoteTokenizerIdentityMismatchFallsBackInRuntime(t *testing.T) {
	identity := remoteTestPromptIdentity()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		changed := identity
		changed.TokenizerRevision = "other"
		writeTestJSON(w, tokenizerResponse{TokenIDs: []TokenID{1}, TokenCount: 1, Identity: changed})
	}))
	defer server.Close()
	tokenizer := RemoteTokenizer{TokenizerID: TokenizerIdentity{ID: "tok", Revision: "tok-rev"}, URL: server.URL, ExpectedIdentity: identity}
	cacheIdentity := CacheIdentity{ProtocolVersion: PrefixProtocolVersion, ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion, BlockSizeTokens: 4, CacheFormatVersion: "vllm-v1", KVLayout: "fp16"}
	runtime := Runtime{Builder: PromptBuilder{Identity: identity, MaxBytes: 1024}, Tokenizer: tokenizer, Identity: cacheIdentity}
	features, err := runtime.Prepare(context.Background(), []PromptMessage{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if !features.TokenizerFallback || len(features.PrefixBlocks) != 0 {
		t.Fatalf("features=%+v", features)
	}
}

func TestRemoteTokenizerTimeoutFallsBackInRuntime(t *testing.T) {
	identity := remoteTestPromptIdentity()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		writeTestJSON(w, tokenizerResponse{TokenIDs: []TokenID{1}, TokenCount: 1, Identity: identity})
	}))
	defer server.Close()
	tokenizer := RemoteTokenizer{TokenizerID: TokenizerIdentity{ID: "tok", Revision: "tok-rev"}, URL: server.URL, ExpectedIdentity: identity, Timeout: time.Millisecond}
	cacheIdentity := CacheIdentity{ProtocolVersion: PrefixProtocolVersion, ModelID: identity.ModelID, ModelRevision: identity.ModelRevision, TokenizerID: identity.TokenizerID, TokenizerRevision: identity.TokenizerRevision, ChatTemplateVersion: identity.ChatTemplateVersion, BlockSizeTokens: 4, CacheFormatVersion: "vllm-v1", KVLayout: "fp16"}
	runtime := Runtime{Builder: PromptBuilder{Identity: identity, MaxBytes: 1024}, Tokenizer: tokenizer, Identity: cacheIdentity}
	features, err := runtime.Prepare(context.Background(), []PromptMessage{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if !features.TokenizerFallback {
		t.Fatalf("features=%+v", features)
	}
}

func TestRemoteTokenizerRejectsOversizedResponse(t *testing.T) {
	identity := remoteTestPromptIdentity()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"token_ids":[1,2,3],"token_count":3,"identity":{"model_id":"model","model_revision":"rev","tokenizer_id":"tok","tokenizer_revision":"tok-rev","chat_template_version":"chat-v1","adapter_id":""}}`))
	}))
	defer server.Close()
	tokenizer := RemoteTokenizer{TokenizerID: TokenizerIdentity{ID: "tok", Revision: "tok-rev"}, URL: server.URL, ExpectedIdentity: identity, MaxResponseBytes: 4}
	if _, err := tokenizer.Encode(context.Background(), "hello"); !errors.Is(err, ErrTokenizerUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
