package posthogmcp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMiddlewareHandlesMissingCapabilityWithoutPriorListing(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{ReportMissing: true, EnableConversationID: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	clientSession := connectInMemory(t, server)

	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      DefaultMissingCapabilityToolName,
		Arguments: map[string]any{"context": "Need a database query tool for SQL operations."},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "noted your feedback") {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Content) != 1 {
		t.Fatalf("unadvertised conversation handle was returned: %#v", result.Content)
	}
}

func TestMiddlewareDoesNotInterceptCollidingRealMissingTool(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{ReportMissing: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: DefaultMissingCapabilityToolName}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "real: " + input.Value}}}, nil, nil
	})
	clientSession := connectInMemory(t, server)

	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      DefaultMissingCapabilityToolName,
		Arguments: map[string]any{"value": "kept"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := result.Content[0].(*mcp.TextContent)
	if text.Text != "real: kept" {
		t.Fatalf("text = %q", text.Text)
	}
}

func TestMiddlewareDoesNotShadowLateRegisteredMissingTool(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{ReportMissing: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	clientSession := connectInMemory(t, server)

	listing, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	findTool(t, listing.Tools, DefaultMissingCapabilityToolName)
	virtual, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: DefaultMissingCapabilityToolName,
		Arguments: map[string]any{
			"context": "Need a specialized capability before its real implementation becomes available to this active server session.",
		},
	})
	if err != nil || len(virtual.Content) != 1 {
		t.Fatalf("virtual result=%#v error=%v", virtual, err)
	}

	mcp.AddTool(server, &mcp.Tool{Name: DefaultMissingCapabilityToolName}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "real: " + input.Value}}}, nil, nil
	})
	real, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      DefaultMissingCapabilityToolName,
		Arguments: map[string]any{"value": "late"},
	})
	if err != nil {
		t.Fatalf("late CallTool: %v", err)
	}
	text := real.Content[0].(*mcp.TextContent)
	if text.Text != "real: late" {
		t.Fatalf("late text = %q", text.Text)
	}
}

func TestAdvertisedMissingToolResolvesIdentityOnce(t *testing.T) {
	t.Parallel()
	var identityCalls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{
		ReportMissing:        true,
		EnableConversationID: true,
		Identify: func(context.Context, mcp.Request) (*Identity, error) {
			identityCalls.Add(1)
			return &Identity{DistinctID: "customer"}, nil
		},
	})
	server.AddReceivingMiddleware(analytics.Middleware())
	clientSession := connectInMemory(t, server)
	if _, err := clientSession.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	before := identityCalls.Load()
	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: DefaultMissingCapabilityToolName,
		Arguments: map[string]any{
			"context": "Need a specialized capability while preserving exactly one identity resolution for this virtual tool invocation.",
		},
	})
	if err != nil || result.IsError {
		t.Fatalf("CallTool result=%#v error=%v", result, err)
	}
	if got := identityCalls.Load() - before; got != 1 {
		t.Fatalf("identity calls = %d, want exactly one for missing invocation", got)
	}
}
