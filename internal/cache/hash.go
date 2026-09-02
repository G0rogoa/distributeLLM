package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const PrefixProtocolVersion = "distserve-prefix-v1"

type BlockHash [32]byte

func (h BlockHash) String() string { return hex.EncodeToString(h[:]) }

type CacheIdentity struct {
	ProtocolVersion     string `json:"protocol_version"`
	ModelID             string `json:"model_id"`
	ModelRevision       string `json:"model_revision"`
	TokenizerID         string `json:"tokenizer_id"`
	TokenizerRevision   string `json:"tokenizer_revision"`
	ChatTemplateVersion string `json:"chat_template_version"`
	AdapterID           string `json:"adapter_id,omitempty"`
	BlockSizeTokens     int    `json:"block_size_tokens"`
	CacheFormatVersion  string `json:"cache_format_version"`
	KVLayout            string `json:"kv_layout"`
}

type PrefixBlock struct {
	Index      int       `json:"index"`
	TokenCount int       `json:"token_count"`
	PrefixHash BlockHash `json:"prefix_hash"`
	ParentHash BlockHash `json:"parent_hash"`
}

type RoutingHint struct {
	Identity               CacheIdentity `json:"identity"`
	PrefixBlocks           []PrefixBlock `json:"prefix_blocks"`
	TotalInputTokens       int           `json:"total_input_tokens"`
	PredictedMatchedBlocks int           `json:"predicted_matched_blocks"`
	PredictedMatchedTokens int           `json:"predicted_matched_tokens"`
}

func (identity CacheIdentity) Validate() error {
	if identity.ProtocolVersion == "" || identity.ModelID == "" || identity.ModelRevision == "" || identity.TokenizerID == "" || identity.TokenizerRevision == "" || identity.ChatTemplateVersion == "" || identity.BlockSizeTokens < 1 || identity.CacheFormatVersion == "" || identity.KVLayout == "" {
		return ErrInvalidIdentity
	}
	return nil
}

func (identity CacheIdentity) Hash() (BlockHash, error) {
	if err := identity.Validate(); err != nil {
		return BlockHash{}, err
	}
	encoded, err := encodeIdentity(identity)
	if err != nil {
		return BlockHash{}, err
	}
	return sha256.Sum256(encoded), nil
}

func BuildPrefixChain(identity CacheIdentity, blocks []TokenBlock) ([]PrefixBlock, error) {
	parent, err := identity.Hash()
	if err != nil {
		return nil, err
	}
	identityBytes, err := encodeIdentity(identity)
	if err != nil {
		return nil, err
	}
	result := make([]PrefixBlock, 0, len(blocks))
	for _, block := range blocks {
		if !block.Full {
			break
		}
		if block.Index != len(result) || len(block.TokenIDs) != identity.BlockSizeTokens {
			return nil, fmt.Errorf("invalid token block at index %d", block.Index)
		}
		var payload bytes.Buffer
		writeString(&payload, "distserve-prefix-block-v1")
		writeBytes(&payload, identityBytes)
		writeBytes(&payload, parent[:])
		writeUint32(&payload, uint32(block.Index))
		writeUint32(&payload, uint32(len(block.TokenIDs)))
		for _, token := range block.TokenIDs {
			writeUint32(&payload, uint32(token))
		}
		hash := sha256.Sum256(payload.Bytes())
		result = append(result, PrefixBlock{Index: block.Index, TokenCount: len(block.TokenIDs), PrefixHash: hash, ParentHash: parent})
		parent = hash
	}
	return result, nil
}

func encodeIdentity(identity CacheIdentity) ([]byte, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	writeString(&encoded, "distserve-cache-identity-v1")
	writeString(&encoded, identity.ProtocolVersion)
	writeString(&encoded, identity.ModelID)
	writeString(&encoded, identity.ModelRevision)
	writeString(&encoded, identity.TokenizerID)
	writeString(&encoded, identity.TokenizerRevision)
	writeString(&encoded, identity.ChatTemplateVersion)
	writeString(&encoded, identity.AdapterID)
	writeUint32(&encoded, uint32(identity.BlockSizeTokens))
	writeString(&encoded, identity.CacheFormatVersion)
	writeString(&encoded, identity.KVLayout)
	return encoded.Bytes(), nil
}

func writeString(buffer *bytes.Buffer, value string) { writeBytes(buffer, []byte(value)) }

func writeBytes(buffer *bytes.Buffer, value []byte) {
	writeUint32(buffer, uint32(len(value)))
	buffer.Write(value)
}

func writeUint32(buffer *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	buffer.Write(encoded[:])
}
