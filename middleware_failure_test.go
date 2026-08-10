package posthogmcp

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

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
