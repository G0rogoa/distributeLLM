package backend

import (
	"distserve/internal/api"
	"distserve/internal/cache"
)

// ChatCompletionRequest is Controller-to-Worker transport, not the public API DTO.
type ChatCompletionRequest struct {
	api.ChatCompletionRequest
	CacheHint *cache.RoutingHint `json:"distserve_cache,omitempty"`
}
