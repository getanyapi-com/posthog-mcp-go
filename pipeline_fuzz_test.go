package posthogmcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"unicode/utf8"

	posthogmcp "github.com/getanyapi-com/posthog-mcp-go"
)

func FuzzCapturePrivacyPipeline(f *testing.F) {
	// Corpus seeds represent ordinary text, a PostHog token, invalid UTF-8, and
	// encoded binary. They exercise the public seam during normal go test runs.
	f.Add([]byte("ordinary text"))
	f.Add([]byte("phx_abcdefghijklmnopqrstuvwxyz123"))
	f.Add([]byte{0xff, 0xfe, 0xfd})
	f.Add([]byte("data:image/png;base64,QUFBQQ=="))

	f.Fuzz(func(t *testing.T, input []byte) {
		client := &recordingClient{}
		analytics := posthogmcp.New(client, nil)
		properties := map[string]any{
			"value": string(input),
			"nested": []any{map[string]any{
				"authorization": string(input),
			}},
		}
		if err := analytics.Capture(context.Background(), "fuzz", properties); err != nil {
			t.Fatalf("Capture() error = %v", err)
		}
		if len(client.messages) == 0 {
			return
		}
		capture := onlyCapture(t, client)
		if value, ok := capture.Properties["value"].(string); ok && !utf8.ValidString(value) {
			t.Fatal("captured property contains invalid UTF-8")
		}
		wire, err := json.Marshal(capture)
		if err != nil {
			t.Fatalf("json.Marshal(capture): %v", err)
		}
		if len(wire) > 102_400 {
			t.Fatalf("capture size = %d, want <= 102400", len(wire))
		}
	})
}
