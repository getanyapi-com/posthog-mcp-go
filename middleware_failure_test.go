package posthogmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMiddlewarePreservesDownstreamResultErrorAndPanic(t *testing.T) {
	t.Parallel()
	analytics := New(middlewareTestClient{}, nil)
	request := &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{}}
	wantResult := &mcp.ListResourcesResult{}
	wantErr := errors.New("downstream")
	calls := 0
	errorHandler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return nil, wantErr
	})
	gotResult, gotErr := errorHandler(t.Context(), "resources/list", request)
	if gotResult != nil || gotErr != wantErr || calls != 1 {
		t.Fatalf("result=%p error=%v calls=%d", gotResult, gotErr, calls)
	}
	resultHandler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return wantResult, nil
	})
	gotResult, gotErr = resultHandler(t.Context(), "resources/list", request)
	if gotResult != wantResult || gotErr != nil || calls != 2 {
		t.Fatalf("result=%p error=%v calls=%d", gotResult, gotErr, calls)
	}

	panicValue := &struct{ message string }{"tool panic"}
	panicking := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		panic(panicValue)
	})
	defer func() {
		if recovered := recover(); recovered != panicValue {
			t.Fatalf("panic = %#v, want original %#v", recovered, panicValue)
		}
	}()
	_, _ = panicking(t.Context(), "resources/list", request)
}

func TestMiddlewarePreparationPanicCannotFailToolCall(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	analytics := New(middlewareTestClient{}, &Options{
		Logger: slog.New(panickingLogHandler{}),
		IntentFallback: func(context.Context, mcp.Request) (string, error) {
			panic("intent callback")
		},
	})
	server.AddReceivingMiddleware(analytics.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "safe"}, func(_ context.Context, _ *mcp.CallToolRequest, input middlewareInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: input.Value}}}, nil, nil
	})
	session := connectInMemory(t, server)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "safe", Arguments: map[string]any{"value": "unchanged"}})
	if err != nil || result.IsError {
		t.Fatalf("CallTool result=%#v error=%v", result, err)
	}
}

func TestTypedNilRequestDelegatesExactlyOnce(t *testing.T) {
	analytics := New(middlewareTestClient{}, nil)
	var request *mcp.ServerRequest[*mcp.ListResourcesParams]
	calls := 0
	want := &mcp.ListResourcesResult{}
	handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return want, nil
	})
	result, err := handler(t.Context(), "resources/list", request)
	if err != nil || result != want || calls != 1 {
		t.Fatalf("result=%p error=%v calls=%d", result, err, calls)
	}
}

func TestHooksCannotMutateDownstreamRequest(t *testing.T) {
	analytics := New(middlewareTestClient{}, &Options{Identify: func(_ context.Context, request mcp.Request) (*Identity, error) {
		request.GetParams().(*mcp.ListResourcesParams).Cursor = "mutated"
		return &Identity{DistinctID: "customer"}, nil
	}})
	request := &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{Cursor: "original"}}
	handler := analytics.Middleware()(func(_ context.Context, _ string, request mcp.Request) (mcp.Result, error) {
		if got := request.GetParams().(*mcp.ListResourcesParams).Cursor; got != "original" {
			t.Fatalf("downstream cursor = %q", got)
		}
		return &mcp.ListResourcesResult{}, nil
	})
	if _, err := handler(t.Context(), "resources/list", request); err != nil {
		t.Fatal(err)
	}
	if request.Params.Cursor != "original" {
		t.Fatalf("caller cursor = %q", request.Params.Cursor)
	}
}

func TestMissingCollisionCallsOrdinaryDownstreamOnce(t *testing.T) {
	analytics := New(middlewareTestClient{}, &Options{ReportMissing: true})
	calls := 0
	want := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "real"}}}
	handler := analytics.Middleware()(func(_ context.Context, method string, _ mcp.Request) (mcp.Result, error) {
		calls++
		if method != "tools/call" {
			t.Fatalf("unexpected downstream method %q", method)
		}
		return want, nil
	})
	request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: DefaultMissingCapabilityToolName}}
	result, err := handler(t.Context(), "tools/call", request)
	if err != nil || result != want || calls != 1 {
		t.Fatalf("result=%p error=%v calls=%d", result, err, calls)
	}
}

func TestMissingCollisionPreservesMisleadingDownstreamError(t *testing.T) {
	analytics := New(middlewareTestClient{}, &Options{ReportMissing: true})
	wantErr := errors.New(`backend failed: unknown tool "get_more_tools" while proxying`)
	calls := 0
	handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return nil, wantErr
	})
	request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: DefaultMissingCapabilityToolName}}
	result, err := handler(t.Context(), "tools/call", request)
	if result != nil || err != wantErr || calls != 1 {
		t.Fatalf("result=%#v error=%v calls=%d", result, err, calls)
	}
}

func TestMissingCollisionPreservesNonCanonicalJSONRPCErrors(t *testing.T) {
	canonical := &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: `unknown tool "get_more_tools"`}
	tests := []struct {
		name string
		err  error
	}{
		{name: "wrapped", err: fmt.Errorf("proxy: %w", canonical)},
		{name: "data", err: &jsonrpc.Error{Code: canonical.Code, Message: canonical.Message, Data: json.RawMessage(`{"source":"proxy"}`)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			analytics := New(middlewareTestClient{}, &Options{ReportMissing: true})
			handler := analytics.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
				return nil, test.err
			})
			request := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: DefaultMissingCapabilityToolName}}
			result, err := handler(t.Context(), "tools/call", request)
			if result != nil || err != test.err {
				t.Fatalf("result=%#v error=%v, want original %v", result, err, test.err)
			}
		})
	}
}

func TestMiddlewareDeduplicatesSameAnalyticsAndFansOutDistinctObjects(t *testing.T) {
	t.Parallel()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "1.0.0"}, nil)
	var firstCalls atomic.Int32
	first := New(middlewareTestClient{}, &Options{IntentFallback: func(context.Context, mcp.Request) (string, error) {
		firstCalls.Add(1)
		return "fallback", nil
	}})
	var secondCalls atomic.Int32
	second := New(middlewareTestClient{}, &Options{IntentFallback: func(context.Context, mcp.Request) (string, error) {
		secondCalls.Add(1)
		return "fallback", nil
	}})
	server.AddReceivingMiddleware(first.Middleware(), first.Middleware(), second.Middleware())
	mcp.AddTool(server, &mcp.Tool{Name: "fanout"}, func(context.Context, *mcp.CallToolRequest, middlewareInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	session := connectInMemory(t, server)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "fanout", Arguments: map[string]any{"value": "ok"}})
	if err != nil || result.IsError {
		t.Fatalf("CallTool result=%#v error=%v", result, err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("fallback calls first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
}

type panickingLogHandler struct{}

func (panickingLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (panickingLogHandler) Handle(context.Context, slog.Record) error {
	panic("logger")
}
func (handler panickingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler panickingLogHandler) WithGroup(string) slog.Handler      { return handler }
