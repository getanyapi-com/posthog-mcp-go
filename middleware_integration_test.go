package posthogmcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

type middlewareTestClient struct{}

func (middlewareTestClient) Enqueue(posthog.Message) error { return nil }

type middlewareInput struct {
	Value string `json:"value"`
}

type structuredOutput struct {
	Value string `json:"value"`
}

func TestMiddlewareConversationIDIsOptionalStrippedAndReturned(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{EnableConversationID: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "structured"}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, structuredOutput, error) {
		return nil, structuredOutput{Value: input.Value}, nil
	})
	clientSession := connectInMemory(t, server)

	listing, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	tool := findTool(t, listing.Tools, "structured")
	inputSchema := tool.InputSchema.(map[string]any)
	if _, ok := inputSchema["properties"].(map[string]any)["conversation_id"]; !ok {
		t.Fatal("conversation_id was not advertised")
	}
	outputSchema := tool.OutputSchema.(map[string]any)
	if _, ok := outputSchema["properties"].(map[string]any)[instructionsKey]; !ok {
		t.Fatal("output instructions were not advertised")
	}

	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "structured", Arguments: map[string]any{"value": "kept"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	structured := result.StructuredContent.(map[string]any)
	instructions := structured[instructionsKey].(map[string]any)
	conversationID := instructions["conversation_id"].(string)
	if !conversationIDPattern.MatchString(conversationID) {
		t.Fatalf("conversation_id = %q", conversationID)
	}
	last := result.Content[len(result.Content)-1].(*mcp.TextContent)
	if !strings.Contains(last.Text, "conversation_id="+conversationID) {
		t.Fatalf("prompt-back = %q", last.Text)
	}
}

func TestMiddlewareInjectsAndStripsOwnedContext(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, nil)
	server.AddReceivingMiddleware(analytics.Middleware())

	var received middlewareInput
	mcp.AddTool(server, &mcp.Tool{Name: "echo"}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, any, error) {
		received = input
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Value}}}, nil, nil
	})

	clientSession := connectInMemory(t, server)
	listing, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	tool := findTool(t, listing.Tools, "echo")
	schema := tool.InputSchema.(map[string]any)
	properties := schema["properties"].(map[string]any)
	contextSchema := properties["context"].(map[string]any)
	if got := contextSchema["description"]; got != DefaultContextDescription {
		t.Fatalf("context description = %q, want %q", got, DefaultContextDescription)
	}
	if _, exists := schema["additionalProperties"]; exists {
		t.Fatal("additionalProperties:false still rejects the injected context field")
	}
	if !containsString(schema["required"], "context") {
		t.Fatal("context was not required")
	}

	result, err := clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "echo",
		Arguments: map[string]any{
			"value":   "kept",
			"context": "Calling echo to verify analytics context is not passed into application validation.",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool returned application error: %#v", result.Content)
	}
	if received.Value != "kept" {
		t.Fatalf("typed handler input = %#v", received)
	}
}

func TestMiddlewareSkipsUnsafeSchemaWithoutMutation(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{EnableConversationID: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	original := map[string]any{"type": "object", "oneOf": []any{map[string]any{"type": "object"}}}
	server.AddTool(&mcp.Tool{Name: "complex", InputSchema: original}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	clientSession := connectInMemory(t, server)
	listing, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	listed := findTool(t, listing.Tools, "complex").InputSchema.(map[string]any)
	if _, exists := listed["properties"]; exists {
		t.Fatalf("complex schema was extended: %#v", listed)
	}
	want := map[string]any{"type": "object", "oneOf": []any{map[string]any{"type": "object"}}}
	if !reflect.DeepEqual(original, want) {
		t.Fatalf("server-owned schema mutated: %#v", original)
	}
}

func TestMiddlewarePreservesCustomerOwnedContext(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{EnableConversationID: true})
	server.AddReceivingMiddleware(analytics.Middleware())
	var received map[string]any
	server.AddTool(&mcp.Tool{
		Name: "owned",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{"type": "string", "description": "Application context"},
			},
			"additionalProperties": true,
		},
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := json.Unmarshal(request.Params.Arguments, &received); err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
	clientSession := connectInMemory(t, server)
	listing, err := clientSession.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	schema := findTool(t, listing.Tools, "owned").InputSchema.(map[string]any)
	properties := schema["properties"].(map[string]any)
	if got := properties["context"].(map[string]any)["description"]; got != "Application context" {
		t.Fatalf("customer context description = %q", got)
	}
	if _, ok := properties["conversation_id"]; !ok {
		t.Fatal("conversation_id was not independently injected")
	}
	_, err = clientSession.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "owned",
		Arguments: map[string]any{
			"context":         "application value",
			"conversation_id": "019fd2b0-3333-7333-8333-333333333333",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if received["context"] != "application value" {
		t.Fatalf("handler arguments = %#v", received)
	}
	if _, exists := received["conversation_id"]; exists {
		t.Fatalf("analytics-owned conversation_id reached handler: %#v", received)
	}
}

func connectInMemory(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})
	return clientSession
}

func findTool(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func containsString(value any, want string) bool {
	items, _ := value.([]any)
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
