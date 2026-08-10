// Portions of this file are derived from agentcathq/agentcat-typescript-sdk
// (formerly MCPCat/mcpcat-typescript-sdk), licensed under the MIT License.
package posthogmcp

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"
)

var fallbackIDSequence atomic.Uint64

func newPrefixedID(prefix string) string {
	id, err := uuid.NewV7()
	if err != nil {
		id = fallbackUUIDv7()
	}
	return prefix + "_" + id.String()
}

func fallbackUUIDv7() uuid.UUID {
	var id uuid.UUID
	milliseconds := uint64(time.Now().UnixMilli())
	id[0] = byte(milliseconds >> 40)
	id[1] = byte(milliseconds >> 32)
	id[2] = byte(milliseconds >> 24)
	id[3] = byte(milliseconds >> 16)
	id[4] = byte(milliseconds >> 8)
	id[5] = byte(milliseconds)
	sequence := fallbackIDSequence.Add(1)
	binary.BigEndian.PutUint64(id[8:], sequence)
	id[6] = 0x70 | byte(sequence>>8)&0x0f
	id[7] = byte(sequence)
	id[8] = id[8]&0x3f | 0x80
	return id
}

func deriveSessionID(input string) string {
	return deterministicPrefixedID("ses", input)
}

func deterministicPrefixedID(prefix, input string) string {
	return prefix + "_" + fnv1aHex(input) + fnv1aHex(input+"::salt")
}

func fnv1aHex(input string) string {
	h1 := uint32(0x84222325)
	h2 := uint32(0xcbf29ce4)
	for _, codeUnit := range utf16.Encode([]rune(input)) {
		h1 = (h1 ^ uint32(codeUnit)) * 0x000001b3
		h2 = (h2 ^ uint32(codeUnit)) * 0x00000193
	}
	return fmt.Sprintf("%08x%08x", h1, h2)
}
