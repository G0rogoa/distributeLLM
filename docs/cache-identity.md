# Cache identity

Phase 2 cache metadata is namespaced by every value that can change prompt tokens or
the simulated KV layout: protocol version, model ID and revision, tokenizer ID and
revision, chat-template version, adapter ID, block size, and cache-format version.
Exact equality is required; cache entries are never shared across identities.

`PromptBuilder` uses a deterministic, versioned boundary. Each message includes its
role and content with explicit byte lengths, so marker-like user content cannot create
an ambiguous prompt. Sampling values such as temperature, stream, and max output tokens
are absent because they do not change prefill input. If a future engine setting changes
the prefill graph or KV layout, it must become part of `CacheIdentity` before reuse.

The deterministic mock tokenizer splits Unicode letter/digit/underscore runs and emits
punctuation as individual tokens. Each piece is mapped with SHA-256 to a uint32 TokenID.
These IDs are stable test data and are not compatible with any real model tokenizer.
Future real tokenizer adapters replace the `Tokenizer` implementation without changing
block hashing or scheduling interfaces.
