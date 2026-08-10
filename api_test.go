package posthogmcp_test

import (
	"context"
	"testing"

	posthogmcp "github.com/getanyapi-com/posthog-mcp-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

type recordingClient struct {
	messages []posthog.Message
}

func (c *recordingClient) Enqueue(message posthog.Message) error {
	c.messages = append(c.messages, message)
	return nil
}

func TestAnalyticsDisabledWithoutClient(t *testing.T) {
	analytics := posthogmcp.New(nil, nil)

	if analytics.Enabled() {
		t.Fatal("Analytics.Enabled() = true, want false")
	}
	if middleware := analytics.Middleware(); middleware == nil {
		t.Fatal("Analytics.Middleware() = nil")
	} else {
		var _ mcp.Middleware = middleware
	}
	if err := analytics.Capture(context.Background(), "feedback_submitted", nil); err != nil {
		t.Fatalf("Analytics.Capture() error = %v, want nil", err)
	}
}

func TestAnalyticsCapturesCustomEvent(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)

	err := analytics.Capture(context.Background(), "feedback_submitted", map[string]any{"rating": 5})
	if err != nil {
		t.Fatalf("Analytics.Capture() error = %v", err)
	}
	if len(client.messages) != 1 {
		t.Fatalf("captured messages = %d, want 1", len(client.messages))
	}
	capture, ok := client.messages[0].(posthog.Capture)
	if !ok {
		t.Fatalf("captured message type = %T, want posthog.Capture", client.messages[0])
	}
	if capture.Event != "feedback_submitted" {
		t.Errorf("event = %q, want feedback_submitted", capture.Event)
	}
	if capture.Properties[posthogmcp.PropertySource] != posthogmcp.Source {
		t.Errorf("source = %v, want %q", capture.Properties[posthogmcp.PropertySource], posthogmcp.Source)
	}
	if capture.Properties["rating"] != 5 {
		t.Errorf("rating = %v, want 5", capture.Properties["rating"])
	}
}
