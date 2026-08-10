package posthogmcp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

func TestGeneratedSessionRollsAfterUpstreamInactivityWindow(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	analytics.now = func() time.Time { return now }

	first := analytics.resolveAttribution(nil, "").sessionID
	now = now.Add(30 * time.Minute)
	if got := analytics.resolveAttribution(nil, "").sessionID; got != first {
		t.Fatalf("session at exact inactivity window = %q, want %q", got, first)
	}

	analytics.mu.Lock()
	analytics.lastActivity = now.Add(-30*time.Minute - time.Nanosecond)
	analytics.mu.Unlock()
	if got := analytics.resolveAttribution(nil, "").sessionID; got == first {
		t.Fatalf("session after inactivity window = %q, want rollover", got)
	}
}

func TestConversationSessionPrecedesGeneratedSession(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	conversationID := "01989f0d-16b7-7c38-a74b-4b85470b6ad8"

	got := analytics.resolveAttribution(nil, conversationID)
	if got.sessionID != deriveSessionID(conversationID) {
		t.Errorf("conversation session = %q, want %q", got.sessionID, deriveSessionID(conversationID))
	}
	if got.conversationID != conversationID {
		t.Errorf("conversation id = %q, want %q", got.conversationID, conversationID)
	}
}

func TestInvalidConversationIDDoesNotOverrideSession(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	baseline := analytics.resolveAttribution(nil, "").sessionID

	got := analytics.resolveAttribution(nil, "not-a-uuidv7")
	if got.sessionID != baseline {
		t.Errorf("invalid conversation session = %q, want generated %q", got.sessionID, baseline)
	}
	if got.conversationID != "" {
		t.Errorf("invalid conversation id = %q, want empty", got.conversationID)
	}
}

func TestGeneratedSessionsDoNotCrossAttributeDistinctSDKConnections(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	seen := make(chan string, 2)
	server := mcp.NewServer(&mcp.Implementation{Name: "server", Version: "1"}, nil)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				seen <- analytics.resolveAttribution(request, "").sessionID
			}
			return next(ctx, method, request)
		}
	})

	for _, name := range []string{"client-a", "client-b"} {
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		serverSession, err := server.Connect(t.Context(), serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = serverSession.Close() })
		client := mcp.NewClient(&mcp.Implementation{Name: name, Version: "1"}, nil)
		clientSession, err := client.Connect(t.Context(), clientTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = clientSession.Close() })
		if _, err := clientSession.ListTools(t.Context(), nil); err != nil {
			t.Fatal(err)
		}
	}

	first, second := <-seen, <-seen
	if first == second {
		t.Fatalf("two SDK connections shared generated session %q", first)
	}
}

func TestConcurrentFallbackRolloverAndCapture(t *testing.T) {
	analytics := New(testEnqueueClient{}, nil)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	analytics.now = func() time.Time { return now }
	analytics.mu.Lock()
	analytics.lastActivity = now.Add(-generatedSessionInactivity - time.Nanosecond)
	analytics.mu.Unlock()
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := analytics.Capture(context.Background(), "concurrent", nil); err != nil {
				t.Errorf("Capture: %v", err)
			}
		}()
	}
	wait.Wait()
}

type testEnqueueClient struct{}

func (testEnqueueClient) Enqueue(posthog.Message) error { return nil }
