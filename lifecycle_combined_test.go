package posthogmcp

import (
	"context"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

type synchronizedCaptureClient struct {
	mu       sync.Mutex
	captures []posthog.Capture
}

func (client *synchronizedCaptureClient) Enqueue(message posthog.Message) error {
	capture, ok := message.(posthog.Capture)
	if !ok {
		return nil
	}
	client.mu.Lock()
	client.captures = append(client.captures, capture)
	client.mu.Unlock()
	return nil
}

func (client *synchronizedCaptureClient) snapshot() []posthog.Capture {
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]posthog.Capture(nil), client.captures...)
}

func TestCombinedLifecycleAttributesIdentityCustomCaptureAndToolException(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		return &Identity{DistinctID: "customer-1", Properties: posthog.Properties{"plan": "free"}}, nil
	}})
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture-server", Version: "1.2.3"}, nil)
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "fails"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ middlewareInput) (*mcp.CallToolResult, any, error) {
		if err := analytics.Capture(ctx, "inside_tool", map[string]any{"safe": true}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fixture failed"}}}, nil, nil
	})
	session := connectInMemory(t, server)
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "fails", Arguments: map[string]any{"value": "kept"}}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	captures := client.snapshot()
	initialize := findCapture(t, captures, EventInitialize)
	if initialize.Properties[PropertyServerName] != "fixture-server" || initialize.Properties[PropertyServerVersion] != "1.2.3" {
		t.Fatalf("initialize server attribution = %#v", initialize.Properties)
	}
	sessionID, ok := initialize.Properties[PropertySessionID].(string)
	if !ok || sessionID == "" {
		t.Fatalf("initialize session = %#v", initialize.Properties[PropertySessionID])
	}
	if initialize.DistinctId != "customer-1" {
		t.Fatalf("initialize distinct id = %q", initialize.DistinctId)
	}
	if countCaptures(captures, EventIdentify) != 1 {
		t.Fatalf("identify count = %d, want 1", countCaptures(captures, EventIdentify))
	}
	custom := findCapture(t, captures, "inside_tool")
	if custom.DistinctId != "customer-1" || custom.Properties[PropertySessionID] != sessionID {
		t.Fatalf("custom attribution = distinct %q properties %#v", custom.DistinctId, custom.Properties)
	}
	tool := findCapture(t, captures, EventToolCall)
	if tool.Properties[PropertyIsError] != true || tool.Properties[PropertyErrorMessage] != "fixture failed" {
		t.Fatalf("tool failure = %#v", tool.Properties)
	}
	exception := findCapture(t, captures, EventException)
	if exception.DistinctId != "customer-1" || exception.Properties[PropertySessionID] != sessionID {
		t.Fatalf("exception attribution = distinct %q properties %#v", exception.DistinctId, exception.Properties)
	}
}

func TestCombinedToolPanicIsCapturedAndRepanickedUnchanged(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, nil)
	panicValue := &struct{ label string }{label: "unchanged"}
	handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic(panicValue)
	})
	request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "panics"}}

	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("panic = %#v, want original %#v", recovered, panicValue)
			}
		}()
		_, _ = handler(t.Context(), "tools/call", request)
	}()

	captures := client.snapshot()
	tool := findCapture(t, captures, EventToolCall)
	if tool.Properties[PropertyIsError] != true {
		t.Fatalf("tool panic properties = %#v", tool.Properties)
	}
	findCapture(t, captures, EventException)
}

func TestUnsupportedMethodDoesNotConsumeIdentityEmission(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		return &Identity{DistinctID: "customer-1"}, nil
	}})
	handler := analytics.Middleware()(func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		if method == "resources/list" {
			return &mcp.ListResourcesResult{}, nil
		}
		return nil, nil
	})
	request := &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{}}
	if _, err := handler(t.Context(), "ping", request); err != nil {
		t.Fatal(err)
	}
	if len(client.snapshot()) != 0 {
		t.Fatalf("unsupported method emitted analytics: %#v", client.snapshot())
	}
	if _, err := handler(t.Context(), "resources/list", request); err != nil {
		t.Fatal(err)
	}
	if countCaptures(client.snapshot(), EventIdentify) != 1 {
		t.Fatalf("identify count = %d, want 1", countCaptures(client.snapshot(), EventIdentify))
	}
}

func findCapture(t *testing.T, captures []posthog.Capture, event string) posthog.Capture {
	t.Helper()
	for _, capture := range captures {
		if capture.Event == event {
			return capture
		}
	}
	t.Fatalf("event %q not found in %#v", event, captures)
	return posthog.Capture{}
}

func countCaptures(captures []posthog.Capture, event string) int {
	count := 0
	for _, capture := range captures {
		if capture.Event == event {
			count++
		}
	}
	return count
}
