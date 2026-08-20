package cache

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidBlockSize = errors.New("block size must be positive")
	ErrTooManyTokens    = errors.New("token count exceeds limit")
)

type TokenBlock struct {
	Index     int
	TokenIDs  []TokenID
	TokenFrom int
	TokenTo   int
	Full      bool
}

func BuildTokenBlocks(tokens []TokenID, blockSize, maxTokens int) ([]TokenBlock, error) {
	if blockSize < 1 {
		return nil, ErrInvalidBlockSize
	}
	if maxTokens < 1 || len(tokens) > maxTokens {
		return nil, ErrTooManyTokens
	}
	blocks := make([]TokenBlock, 0, (len(tokens)+blockSize-1)/blockSize)
	for from := 0; from < len(tokens); from += blockSize {
		to := from + blockSize
		if to > len(tokens) {
			to = len(tokens)
		}
		blockTokens := append([]TokenID(nil), tokens[from:to]...)
		blocks = append(blocks, TokenBlock{Index: len(blocks), TokenIDs: blockTokens, TokenFrom: from, TokenTo: to, Full: to-from == blockSize})
	}
	if len(blocks) > 0 && blocks[len(blocks)-1].TokenTo != len(tokens) {
		return nil, fmt.Errorf("block builder invariant failed")
	}
	return blocks, nil
}
