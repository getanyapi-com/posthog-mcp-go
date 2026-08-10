package posthogmcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	posthogmcp "github.com/getanyapi-com/posthog-mcp-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

// Fixtures in this file port observable behavior from PostHog/posthog-js
// sanitization.test.ts, truncation.test.ts, capture.test.ts, and
// beforeSend.test.ts at 80f15a386621514c43f19e99ee4e3f702e4d369d.

func TestCaptureSanitizesWithoutMutatingInput(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)
	largeBase64 := strings.Repeat("A", 10_239) + "="
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	properties := map[string]any{
		"authorization": "Bearer private",
		"message":       "key phx_abcdefghijklmnopqrstuvwxyz123 remains",
		"binary":        largeBase64,
		"cycle":         cyclic,
		"response": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "safe"},
			map[string]any{"type": "image", "data": "private"},
			map[string]any{"type": "resource", "resource": map[string]any{"blob": "private"}},
		}},
	}

	if err := analytics.Capture(context.Background(), "privacy_checked", properties); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	capture := onlyCapture(t, client)
	if got := capture.Properties["authorization"]; got != "[redacted]" {
		t.Errorf("authorization = %v, want [redacted]", got)
	}
	if got := capture.Properties["message"]; got != "key [redacted] remains" {
		t.Errorf("message = %v, want token redacted", got)
	}
	if got := capture.Properties["binary"]; got != "[binary data redacted - not supported by PostHog MCP analytics]" {
		t.Errorf("binary = %v, want binary redaction marker", got)
	}
	cycle := capture.Properties["cycle"].(map[string]any)
	if cycle["self"] != "[Circular ~]" {
		t.Error("cycle marker was not [Circular ~]")
	}
	response := capture.Properties["response"].(map[string]any)
	content := response["content"].([]any)
	if got := content[1].(map[string]any)["text"]; got != "[image content redacted - not supported by PostHog MCP analytics]" {
		t.Errorf("image marker = %v", got)
	}
	if got := content[2].(map[string]any)["text"]; got != "[binary resource content redacted - not supported by PostHog MCP analytics]" {
		t.Errorf("resource marker = %v", got)
	}
	if properties["authorization"] != "Bearer private" || properties["binary"] != largeBase64 {
		t.Fatal("Capture mutated caller properties")
	}
}

func TestCaptureAppliesEventPropertiesBeforeBeforeSend(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, &posthogmcp.Options{
		EventProperties: func(context.Context, mcp.Request) (posthog.Properties, error) {
			return posthog.Properties{"from_hook": "visible", "api_key": "private"}, nil
		},
		BeforeSend: func(_ context.Context, event posthogmcp.Event) (*posthogmcp.Event, error) {
			if event.Properties["from_hook"] != "visible" {
				t.Fatal("BeforeSend did not observe EventProperties output")
			}
			if event.Properties["api_key"] != "[redacted]" {
				t.Fatalf("BeforeSend observed unsanitized hook property: %v", event.Properties["api_key"])
			}
			event.Properties["before_send"] = true
			return &event, nil
		},
	})

	if err := analytics.Capture(context.Background(), "ordered", map[string]any{"input": true}); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	capture := onlyCapture(t, client)
	if capture.Properties["before_send"] != true {
		t.Errorf("before_send = %v, want true", capture.Properties["before_send"])
	}
	if capture.Properties[posthogmcp.PropertySource] != posthogmcp.Source {
		t.Errorf("source = %v, want %q", capture.Properties[posthogmcp.PropertySource], posthogmcp.Source)
	}
}

func TestCaptureSnapshotsBeforeSendResult(t *testing.T) {
	client := &recordingClient{}
	var retained *posthogmcp.Event
	analytics := posthogmcp.New(client, &posthogmcp.Options{
		BeforeSend: func(_ context.Context, event posthogmcp.Event) (*posthogmcp.Event, error) {
			retained = &event
			return retained, nil
		},
	})
	if err := analytics.Capture(context.Background(), "snapshot", map[string]any{"state": "captured"}); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	retained.Properties["state"] = "mutated later"
	if got := onlyCapture(t, client).Properties["state"]; got != "captured" {
		t.Fatalf("captured state = %v, want immutable snapshot", got)
	}
}

func TestBeforeSendMutationIsFinalWireValue(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, &posthogmcp.Options{BeforeSend: func(_ context.Context, event posthogmcp.Event) (*posthogmcp.Event, error) {
		event.Properties["api_key"] = "hook-owned-value"
		return &event, nil
	}})
	if err := analytics.Capture(context.Background(), "hook_final", nil); err != nil {
		t.Fatal(err)
	}
	if got := onlyCapture(t, client).Properties["api_key"]; got != "hook-owned-value" {
		t.Fatalf("api_key = %v, want hook-owned final value", got)
	}
}

func TestBeforeSendCyclicMutationDropsOnlyAnalytics(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, &posthogmcp.Options{BeforeSend: func(_ context.Context, event posthogmcp.Event) (*posthogmcp.Event, error) {
		cycle := map[string]any{}
		cycle["self"] = cycle
		event.Properties["cycle"] = cycle
		return &event, nil
	}})
	if err := analytics.Capture(context.Background(), "cyclic_hook", nil); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(client.messages) != 0 {
		t.Fatalf("captured messages = %d, want malformed hook event dropped", len(client.messages))
	}
}

func TestBeforeSendDeepMutationRemainsFinalWireValue(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, &posthogmcp.Options{BeforeSend: func(_ context.Context, event posthogmcp.Event) (*posthogmcp.Event, error) {
		deep := map[string]any{"leaf": "kept"}
		for range 12 {
			deep = map[string]any{"next": deep}
		}
		event.Properties["deep"] = deep
		return &event, nil
	}})
	if err := analytics.Capture(context.Background(), "deep_hook", nil); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(client.messages) != 1 {
		t.Fatalf("captured messages = %d, want trusted deep hook output preserved", len(client.messages))
	}
}

func TestCaptureDropsHookFailuresAndDroppedEvents(t *testing.T) {
	tests := []struct {
		name string
		hook posthogmcp.BeforeSendFunc
	}{
		{name: "drop", hook: func(context.Context, posthogmcp.Event) (*posthogmcp.Event, error) { return nil, nil }},
		{name: "error", hook: func(context.Context, posthogmcp.Event) (*posthogmcp.Event, error) { return nil, errors.New("boom") }},
		{name: "panic", hook: func(context.Context, posthogmcp.Event) (*posthogmcp.Event, error) { panic("boom") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &recordingClient{}
			analytics := posthogmcp.New(client, &posthogmcp.Options{BeforeSend: tt.hook})
			if err := analytics.Capture(context.Background(), "dropped", nil); err != nil {
				t.Fatalf("Capture() error = %v, want fail-open nil", err)
			}
			if len(client.messages) != 0 {
				t.Fatalf("captured messages = %d, want 0", len(client.messages))
			}
		})
	}
}

func TestCaptureIsolatesEventPropertiesAndEnqueueFailures(t *testing.T) {
	tests := []struct {
		name   string
		client posthog.EnqueueClient
		props  posthogmcp.EventPropertiesFunc
	}{
		{name: "properties error", client: &recordingClient{}, props: func(context.Context, mcp.Request) (posthog.Properties, error) { return nil, errors.New("boom") }},
		{name: "properties panic", client: &recordingClient{}, props: func(context.Context, mcp.Request) (posthog.Properties, error) { panic("boom") }},
		{name: "enqueue error", client: failingClient{}},
		{name: "enqueue panic", client: panickingClient{}},
		{name: "logger panic", client: failingClient{}, props: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &posthogmcp.Options{EventProperties: tt.props}
			if tt.name == "logger panic" {
				options.Logger = slog.New(panicHandler{})
			}
			analytics := posthogmcp.New(tt.client, options)
			if err := analytics.Capture(context.Background(), "isolated", nil); err != nil {
				t.Fatalf("Capture() error = %v, want fail-open nil", err)
			}
		})
	}
}

func TestCaptureContinuesWhenEventPropertiesFails(t *testing.T) {
	for _, hook := range []posthogmcp.EventPropertiesFunc{
		func(context.Context, mcp.Request) (posthog.Properties, error) { return nil, errors.New("boom") },
		func(context.Context, mcp.Request) (posthog.Properties, error) { panic("boom") },
	} {
		client := &recordingClient{}
		analytics := posthogmcp.New(client, &posthogmcp.Options{EventProperties: hook})
		if err := analytics.Capture(context.Background(), "continued", nil); err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
		if capture := onlyCapture(t, client); capture.Event != "continued" {
			t.Errorf("event = %q, want continued", capture.Event)
		}
	}
}

func TestCaptureBoundsStringsAndCollections(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)
	items := make([]any, 150)
	for i := range items {
		items[i] = i
	}
	properties := map[string]any{
		"unicode": strings.Repeat("🧪", 10_000),
		"items":   items,
	}

	if err := analytics.Capture(context.Background(), "bounded", properties); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	capture := onlyCapture(t, client)
	gotUnicode := capture.Properties["unicode"].(string)
	if !utf8.ValidString(gotUnicode) || len(gotUnicode) > 32_768+3 {
		t.Fatalf("unicode bytes = %d, valid = %v", len(gotUnicode), utf8.ValidString(gotUnicode))
	}
	gotItems, ok := capture.Properties["items"].([]any)
	if !ok {
		t.Fatalf("items type = %T, want []any", capture.Properties["items"])
	}
	if len(gotItems) != 101 || gotItems[100] != "[MaxProperties ~]" {
		t.Fatalf("items length/marker = %d/%v, want 101/[MaxProperties ~]", len(gotItems), gotItems[100])
	}
	if properties["unicode"] == capture.Properties["unicode"] {
		t.Fatal("Capture did not snapshot/truncate the input")
	}
}

func TestCaptureEnforcesTotalEventBudget(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)
	large := map[string]any{}
	for i := 0; i < 60; i++ {
		large[string(rune('a'+i%26))+strings.Repeat("_", i/26)] = strings.Repeat("z", 5_000)
	}
	if err := analytics.Capture(context.Background(), "bounded", large); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	wire, err := json.Marshal(onlyCapture(t, client))
	if err != nil {
		t.Fatalf("json.Marshal(capture): %v", err)
	}
	if len(wire) > 102_400 {
		t.Fatalf("capture size = %d, want <= 102400", len(wire))
	}
}

func TestCaptureDropsAnIrreducibleOversizeEvent(t *testing.T) {
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)
	properties := map[string]any{}
	for index := 0; index < 100; index++ {
		properties[strings.Repeat(string(rune('a'+index%26)), 2_000)+string(rune(index+256))] = index
	}
	if err := analytics.Capture(context.Background(), "oversize_keys", properties); err != nil {
		t.Fatalf("Capture() error = %v, want fail-open nil", err)
	}
	if len(client.messages) != 0 {
		t.Fatalf("captured messages = %d, want oversized analytics dropped", len(client.messages))
	}
}

func onlyCapture(t *testing.T, client *recordingClient) posthog.Capture {
	t.Helper()
	if len(client.messages) != 1 {
		t.Fatalf("captured messages = %d, want 1", len(client.messages))
	}
	capture, ok := client.messages[0].(posthog.Capture)
	if !ok {
		t.Fatalf("message type = %T, want posthog.Capture", client.messages[0])
	}
	return capture
}

type failingClient struct{}

func (failingClient) Enqueue(posthog.Message) error { return errors.New("queue full") }

type panickingClient struct{}

func (panickingClient) Enqueue(posthog.Message) error { panic("closed") }

type panicHandler struct{}

func (panicHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (panicHandler) Handle(context.Context, slog.Record) error { panic("logger") }
func (panicHandler) WithAttrs([]slog.Attr) slog.Handler        { return panicHandler{} }
func (panicHandler) WithGroup(string) slog.Handler             { return panicHandler{} }
