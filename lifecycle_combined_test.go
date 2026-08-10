package posthogmcp

import (
	"context"
	"sync"
	"sync/atomic"
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
	var metadataCalls atomic.Int32
	var nilMetadataRequests atomic.Int32
	analytics := New(client, &Options{Identify: func(context.Context, mcp.Request) (*Identity, error) {
		return &Identity{DistinctID: "customer-1", Properties: posthog.Properties{"plan": "free"}}, nil
	}, EventProperties: func(_ context.Context, request mcp.Request) (posthog.Properties, error) {
		call := metadataCalls.Add(1)
		if call == 1 && request == nil {
			nilMetadataRequests.Add(1)
		}
		return posthog.Properties{"metadata_call": call}, nil
	}})
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture-server", Version: "1.2.3"}, nil)
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "fails", Description: "Always fails for the fixture.", Meta: mcp.Meta{"category": "Testing"}}, func(ctx context.Context, _ *mcp.CallToolRequest, _ middlewareInput) (*mcp.CallToolResult, any, error) {
		if err := analytics.Capture(ctx, "inside_tool", map[string]any{"safe": true}); err != nil {
			t.Fatalf("Capture: %v", err)
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fixture failed"}}}, nil, nil
	})
	session := connectInMemory(t, server)
	if _, err := session.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "fails", Arguments: map[string]any{
		"value": "kept", "context": "Verify the failing fixture preserves canonical analytics context without changing the tool result.",
	}}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	captures := client.snapshot()
	initialize := findCapture(t, captures, EventInitialize)
	serverName, _ := initialize.Properties[PropertyServerName].(string)
	serverVersion, _ := initialize.Properties[PropertyServerVersion].(string)
	if serverName != "" && (serverName != "fixture-server" || serverVersion != "1.2.3") {
		t.Fatalf("initialize server attribution = %#v", initialize.Properties)
	}
	if initialize.Properties[PropertyProtocolVersion] == "" {
		t.Fatalf("initialize protocol attribution = %#v", initialize.Properties)
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
	if tool.Properties[PropertyToolDescription] != "Always fails for the fixture." || tool.Properties[PropertyToolCategory] != "Testing" {
		t.Fatalf("tool metadata = %#v", tool.Properties)
	}
	parameters := tool.Properties[PropertyParameters].(map[string]any)
	request := parameters["request"].(map[string]any)
	params := request["params"].(map[string]any)
	arguments := params["arguments"].(map[string]any)
	if request["method"] != "tools/call" || params["name"] != "fails" || arguments["value"] != "kept" {
		t.Fatalf("tool parameters = %#v", parameters)
	}
	if arguments["context"] != nil {
		t.Fatalf("wrapper-owned arguments leaked: %#v", arguments)
	}
	exception := findCapture(t, captures, EventException)
	if exception.DistinctId != "customer-1" || exception.Properties[PropertySessionID] != sessionID {
		t.Fatalf("exception attribution = distinct %q properties %#v", exception.DistinctId, exception.Properties)
	}
	if exception.Properties[PropertyToolDescription] != "Always fails for the fixture." || exception.Properties[PropertyToolCategory] != "Testing" {
		t.Fatalf("exception tool metadata = %#v", exception.Properties)
	}
	if metadataCalls.Load() != 4 {
		t.Fatalf("event properties calls = %d, want one custom plus initialize, tools/list, and one tool lifecycle resolution", metadataCalls.Load())
	}
	if nilMetadataRequests.Load() != 0 {
		t.Fatalf("event properties received %d nil requests", nilMetadataRequests.Load())
	}
	for _, event := range []posthog.Capture{tool, exception} {
		if event.Properties["metadata_call"] != tool.Properties["metadata_call"] {
			t.Fatalf("lifecycle siblings have different metadata: %#v", captures)
		}
	}
	identify := findCapture(t, captures, EventIdentify)
	if identify.Properties["metadata_call"] != initialize.Properties["metadata_call"] {
		t.Fatalf("initialize siblings have different metadata: %#v", captures)
	}
}

func TestCapturedToolParametersExcludeAllWrapperOwnedFields(t *testing.T) {
	parameters := buildCapturedToolParameters("fixture", map[string]any{
		"value": "kept", "context": "owned", "conversation_id": "owned",
	}, parameterOwnership{context: true, conversationID: true})
	request := parameters["request"].(map[string]any)
	params := request["params"].(map[string]any)
	arguments := params["arguments"].(map[string]any)
	if len(arguments) != 1 || arguments["value"] != "kept" {
		t.Fatalf("captured arguments = %#v", arguments)
	}
}

func TestIntentFallbackIsCapturedAsInferred(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, &Options{IntentFallback: func(context.Context, mcp.Request) (string, error) {
		return "Inspect fixture state", nil
	}})
	handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "inspect"}}
	if _, err := handler(t.Context(), "tools/call", request); err != nil {
		t.Fatal(err)
	}
	capture := findCapture(t, client.snapshot(), EventToolCall)
	if capture.Properties[PropertyIntent] != "Inspect fixture state" || capture.Properties[PropertyIntentSource] != "inferred" {
		t.Fatalf("intent properties = %#v", capture.Properties)
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

func TestNonToolPanicMarksLifecycleEventAsError(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, nil)
	panicValue := "resource panic"
	handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic(panicValue)
	})
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("panic = %#v", recovered)
			}
		}()
		_, _ = handler(t.Context(), "resources/list", &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{}})
	}()
	capture := findCapture(t, client.snapshot(), EventResourcesList)
	if capture.Properties[PropertyIsError] != true || capture.Properties[PropertyErrorType] != "Panic" {
		t.Fatalf("panic properties = %#v", capture.Properties)
	}
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

func TestLifecycleCapturesResourcePromptAndPaginatedToolEvents(t *testing.T) {
	client := &synchronizedCaptureClient{}
	analytics := New(client, nil)
	handler := analytics.Middleware()(func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/list":
			return &mcp.ListToolsResult{NextCursor: "tools-next", Tools: []*mcp.Tool{{Name: "late"}}}, nil
		case "resources/list":
			return &mcp.ListResourcesResult{NextCursor: "resources-next"}, nil
		case "resources/read":
			return &mcp.ReadResourceResult{}, nil
		case "prompts/list":
			return &mcp.ListPromptsResult{NextCursor: "prompts-next"}, nil
		case "prompts/get":
			return &mcp.GetPromptResult{}, nil
		default:
			return nil, nil
		}
	})

	toolsResult, err := handler(t.Context(), "tools/list", &mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{Cursor: "tools-page"}})
	if err != nil || toolsResult.(*mcp.ListToolsResult).NextCursor != "tools-next" {
		t.Fatalf("tools result = %#v, error = %v", toolsResult, err)
	}
	requests := []struct {
		method  string
		request mcp.Request
	}{
		{"resources/list", &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{Cursor: "resources-page"}}},
		{"resources/read", &mcp.ServerRequest[*mcp.ReadResourceParams]{Params: &mcp.ReadResourceParams{URI: "fixture://resource"}}},
		{"prompts/list", &mcp.ServerRequest[*mcp.ListPromptsParams]{Params: &mcp.ListPromptsParams{Cursor: "prompts-page"}}},
		{"prompts/get", &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Name: "fixture-prompt"}}},
	}
	for _, request := range requests {
		if _, err := handler(t.Context(), request.method, request.request); err != nil {
			t.Fatalf("%s: %v", request.method, err)
		}
	}

	captures := client.snapshot()
	tools := findCapture(t, captures, EventToolsList)
	listed := tools.Properties[PropertyListedToolNames].([]any)
	if len(listed) != 1 || listed[0] != "late" {
		t.Fatalf("listed tools = %#v", listed)
	}
	resource := findCapture(t, captures, EventResourceRead)
	if resource.Properties[PropertyResourceName] != "fixture://resource" {
		t.Fatalf("resource properties = %#v", resource.Properties)
	}
	resourceParameters := resource.Properties[PropertyParameters].(map[string]any)
	resourceRequest := resourceParameters["request"].(map[string]any)
	resourceParams := resourceRequest["params"].(map[string]any)
	if resourceRequest["method"] != "resources/read" || resourceParams["uri"] != "fixture://resource" {
		t.Fatalf("resource parameters = %#v", resourceParameters)
	}
	prompt := findCapture(t, captures, EventPromptGet)
	if prompt.Properties[PropertyResourceName] != "fixture-prompt" {
		t.Fatalf("prompt properties = %#v", prompt.Properties)
	}
	findCapture(t, captures, EventResourcesList)
	findCapture(t, captures, EventPromptsList)
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
