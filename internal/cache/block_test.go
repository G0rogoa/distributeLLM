package cache

import (
	"errors"
	"reflect"
	"testing"
)

func TestBuildTokenBlocksBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		count      int
		wantBlocks int
		lastFull   bool
	}{{"empty", 0, 0, false}, {"partial", 3, 1, false}, {"exact", 4, 1, true}, {"multiple", 8, 2, true}, {"tail", 10, 3, false}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens := make([]TokenID, test.count)
			for i := range tokens {
				tokens[i] = TokenID(i + 1)
			}
			blocks, err := BuildTokenBlocks(tokens, 4, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(blocks) != test.wantBlocks {
				t.Fatalf("blocks=%d", len(blocks))
			}
			if len(blocks) > 0 && blocks[len(blocks)-1].Full != test.lastFull {
				t.Fatalf("last=%+v", blocks[len(blocks)-1])
			}
		})
	}
}

func TestBuildTokenBlocksCopiesTokens(t *testing.T) {
	tokens := []TokenID{1, 2, 3, 4}
	blocks, err := BuildTokenBlocks(tokens, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	blocks[0].TokenIDs[0] = 99
	if !reflect.DeepEqual(tokens, []TokenID{1, 2, 3, 4}) {
		t.Fatalf("caller tokens changed: %v", tokens)
	}
	if blocks[0].TokenFrom != 0 || blocks[0].TokenTo != 2 || blocks[1].TokenFrom != 2 || blocks[1].TokenTo != 4 {
		t.Fatalf("blocks=%+v", blocks)
	}
}

func TestBuildTokenBlocksRejectsInvalidConfiguration(t *testing.T) {
	if _, err := BuildTokenBlocks(nil, 0, 10); !errors.Is(err, ErrInvalidBlockSize) {
		t.Fatalf("got %v", err)
	}
	if _, err := BuildTokenBlocks([]TokenID{1, 2}, 1, 1); !errors.Is(err, ErrTooManyTokens) {
		t.Fatalf("got %v", err)
	}
}
