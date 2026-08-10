package posthogmcp

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestConversationIDFallbackRemainsUUIDv7(t *testing.T) {
	original := newConversationUUIDv7
	newConversationUUIDv7 = func() (uuid.UUID, error) { return uuid.Nil, errors.New("entropy unavailable") }
	t.Cleanup(func() { newConversationUUIDv7 = original })

	resolution := resolveConversationID(true, nil)
	parsed, err := uuid.Parse(resolution.id)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("conversation id = %q, error = %v", resolution.id, err)
	}
}
