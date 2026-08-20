# Prefix hash protocol

The protocol version is `distserve-prefix-v1`. Prompt TokenIDs are divided from token
zero into fixed-size blocks. A partial final block is retained for statistics but is
not a reusable cache unit in the first implementation.

The identity root and each prefix block use SHA-256. Every variable-length field is
encoded as a four-byte unsigned big-endian length followed by its bytes; integers are
four-byte unsigned big-endian values. Field order is fixed in `internal/cache/hash.go`.
This avoids delimiter ambiguity and platform byte-order dependence.

Conceptually:

```text
H0 = SHA256(versioned CacheIdentity encoding)
H1 = SHA256(block-domain || identity || H0 || index || count || tokens(block 0))
H2 = SHA256(block-domain || identity || H1 || index || count || tokens(block 1))
```

The parent hash makes an identical block at a different prefix position a different
cache key. Any token change modifies its block hash and every descendant. A golden test
pins the exact byte protocol; intentional protocol changes require a new protocol/domain
version and migration rather than silently changing existing cache identities.
