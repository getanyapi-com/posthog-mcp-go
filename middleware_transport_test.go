package posthogmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMiddlewareWorksThroughStreamableHTTP(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "http-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, nil)
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "http-tool"}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Value}}}, nil, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(httpServer.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	listing, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	properties := findTool(t, listing.Tools, "http-tool").InputSchema.(map[string]any)["properties"].(map[string]any)
	if _, ok := properties["context"]; !ok {
		t.Fatal("context was not advertised over Streamable HTTP")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "http-tool", Arguments: map[string]any{
		"value": "ok", "context": "Testing real Streamable HTTP middleware behavior through the official MCP Go client.",
	}})
	if err != nil || result.IsError {
		t.Fatalf("CallTool result=%#v error=%v", result, err)
	}
}
