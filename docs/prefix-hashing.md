# Prefix hash protocol

协议版本是 `distserve-prefix-v1`。Prompt `TokenID` 从 token zero 开始按固定大小切成 blocks。最后一个 partial block 会保留用于统计，但在第一版实现中不是可复用 cache unit。

Identity root 和每个 prefix block 都使用 SHA-256。每个变长字段都编码为四字节 unsigned big-endian length，后面跟原始 bytes；整数是四字节 unsigned big-endian。字段顺序固定在 `internal/cache/hash.go` 中。这样可以避免 delimiter 歧义和平台 byte-order 差异。

概念上：

```text
H0 = SHA256(versioned CacheIdentity encoding)
H1 = SHA256(block-domain || identity || H0 || index || count || tokens(block 0))
H2 = SHA256(block-domain || identity || H1 || index || count || tokens(block 1))
```

Parent hash 让不同 prefix 位置上的相同 block 得到不同 cache key。任何 token 改变都会修改它所在 block 的 hash 以及所有后代 hash。Golden test 固定精确 byte protocol；有意改变 protocol 时需要新的 protocol/domain version 和 migration，而不是悄悄改变现有 cache identities。
