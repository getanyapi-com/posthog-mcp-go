package posthogmcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeterministicSessionIDsMatchTypeScriptFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/conformance/session_ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Input    string `json:"input"`
		Expected string `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}

	for _, fixture := range fixtures {
		if got := deriveSessionID(fixture.Input); got != fixture.Expected {
			t.Errorf("deriveSessionID(%q) = %q, want %q", fixture.Input, got, fixture.Expected)
		}
	}
}

func TestNewPrefixedIDUsesUUIDv7(t *testing.T) {
	for _, prefix := range []string{"evt", "ses"} {
		got := newPrefixedID(prefix)
		if !strings.HasPrefix(got, prefix+"_") {
			t.Fatalf("newPrefixedID(%q) = %q", prefix, got)
		}
		parsed, err := uuid.Parse(strings.TrimPrefix(got, prefix+"_"))
		if err != nil {
			t.Fatalf("newPrefixedID(%q) UUID parse: %v", prefix, err)
		}
		if parsed.Version() != 7 {
			t.Errorf("newPrefixedID(%q) version = %d, want 7", prefix, parsed.Version())
		}
	}
}
