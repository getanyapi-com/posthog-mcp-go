package posthogmcp_test

import (
	"context"
	"math"
	"strings"
	"testing"

	posthogmcp "github.com/getanyapi-com/posthog-mcp-go"
)

// Literal vectors port PostHog/posthog-js sanitization.test.ts and
// truncation.test.ts at 80f15a386621514c43f19e99ee4e3f702e4d369d and
// posthog-python test_pipeline.py/test_truncation.py at 6e5389ac.

func TestCaptureSanitizationVectors(t *testing.T) {
	largeBase64URL := strings.Repeat("_", 10_240)
	dataURL := "data:image/png;base64," + strings.Repeat("A", 10_239) + "="
	ordinary := strings.Repeat("ordinary prose! ", 800)
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)

	err := analytics.Capture(context.Background(), "vectors", map[string]any{
		"api-key":   "private",
		"mixedCase": map[string]any{"Client_Secret": "private"},
		"base64url": largeBase64URL,
		"data_url":  dataURL,
		"ordinary":  ordinary,
		"small":     "AAAA=",
		"nan":       math.NaN(),
		"positive":  math.Inf(1),
		"negative":  math.Inf(-1),
		"typed":     map[string]any{"type": "business-object", "value": "preserved"},
	})
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	properties := onlyCapture(t, client).Properties
	if properties["api-key"] != "[redacted]" {
		t.Errorf("api-key = %v", properties["api-key"])
	}
	if properties["mixedCase"].(map[string]any)["Client_Secret"] != "[redacted]" {
		t.Error("case-insensitive sensitive key was not redacted")
	}
	for _, key := range []string{"base64url", "data_url"} {
		if properties[key] != "[binary data redacted - not supported by PostHog MCP analytics]" {
			t.Errorf("%s was not redacted", key)
		}
	}
	if properties["ordinary"] != ordinary || properties["small"] != "AAAA=" {
		t.Error("ordinary or below-gate string changed")
	}
	if properties["nan"] != "[NaN]" || properties["positive"] != "[Infinity]" || properties["negative"] != "[-Infinity]" {
		t.Error("non-finite number markers do not match the upstream contract")
	}
	typed := properties["typed"].(map[string]any)
	if typed["type"] != "business-object" || typed["value"] != "preserved" {
		t.Error("a non-content object with a type field was treated as an MCP content block")
	}
}

func TestCaptureAppliesCanonicalFieldLimits(t *testing.T) {
	frames := make([]any, 80)
	for index := range frames {
		frames[index] = map[string]any{"frame": index}
	}
	client := &recordingClient{}
	analytics := posthogmcp.New(client, nil)
	properties := map[string]any{
		posthogmcp.PropertyIntent:       strings.Repeat("i", 3_000),
		posthogmcp.PropertyErrorMessage: strings.Repeat("e", 3_000),
		posthogmcp.PropertyResourceName: strings.Repeat("r", 500),
		posthogmcp.PropertyClientName:   strings.Repeat("c", 500),
		"$exception_list": []any{map[string]any{
			"value":      strings.Repeat("x", 3_000),
			"stacktrace": map[string]any{"frames": frames},
		}},
	}
	if err := analytics.Capture(context.Background(), "limits", properties); err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	got := onlyCapture(t, client).Properties
	if len(got[posthogmcp.PropertyIntent].(string)) != 2_051 || len(got[posthogmcp.PropertyErrorMessage].(string)) != 2_051 {
		t.Error("intent or error message did not use the 2048-byte limit plus suffix")
	}
	if len(got[posthogmcp.PropertyResourceName].(string)) != 259 || len(got[posthogmcp.PropertyClientName].(string)) != 259 {
		t.Error("resource or metadata field did not use the 256-byte limit plus suffix")
	}
	exception := got["$exception_list"].([]any)[0].(map[string]any)
	if len(exception["value"].(string)) != 2_051 {
		t.Error("exception value did not use the 2048-byte limit plus suffix")
	}
	boundedFrames := exception["stacktrace"].(map[string]any)["frames"].([]any)
	if len(boundedFrames) != 50 || boundedFrames[0].(map[string]any)["frame"] != 0 || boundedFrames[49].(map[string]any)["frame"] != 79 {
		t.Error("exception frames did not preserve the first 25 and last 25")
	}
}
