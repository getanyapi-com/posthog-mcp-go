package posthogmcp

import (
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

func TestClassifyToolFailureCoversProtocolAndErrorResults(t *testing.T) {
	tests := []struct {
		name        string
		result      mcp.Result
		err         error
		message     string
		errorType   string
		shouldError bool
	}{
		{name: "success", result: &mcp.CallToolResult{}, shouldError: false},
		{name: "returned error", err: errors.New("handler failed"), message: "handler failed", errorType: "Error", shouldError: true},
		{
			name: "isError text content",
			result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
				&mcp.TextContent{Text: "part one"}, &mcp.ImageContent{}, &mcp.TextContent{Text: "part two"},
			}},
			message: "part one part two", errorType: "Error", shouldError: true,
		},
		{name: "isError without text", result: &mcp.CallToolResult{IsError: true}, message: "Unknown error", errorType: "Error", shouldError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := classifyToolFailure(test.result, test.err)
			if !test.shouldError {
				if failure != nil {
					t.Fatalf("failure = %#v, want nil", failure)
				}
				return
			}
			if failure == nil || failure.message != test.message || failure.errorType != test.errorType {
				t.Fatalf("failure = %#v, want type %q message %q", failure, test.errorType, test.message)
			}
		})
	}
}

func TestCallToolResultPrefersUnderlyingServerError(t *testing.T) {
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "friendly message"}}}
	result.SetError(namedFixtureError{})

	failure := classifyToolFailure(result, nil)
	if failure.errorType != "Error" || failure.message != "underlying failure" {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestToolFailureBuildsCanonicalMainAndExceptionSibling(t *testing.T) {
	main := Event{
		UUID:       "main-id",
		Event:      EventToolCall,
		DistinctID: "user-1",
		Timestamp:  time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		Groups:     posthog.Groups{"organization": "org-1"},
		Properties: posthog.Properties{
			PropertySessionID:       "ses_1",
			PropertyToolName:        "broken_tool",
			PropertyServerName:      "fixture-server",
			PropertyClientUserAgent: "fixture/1",
			PropertyParameters:      map[string]any{"secret": "kept off sibling"},
		},
	}
	failure := classifyToolFailure(nil, errors.New("boom"))
	sibling := applyToolFailure(&main, failure)

	if main.Properties[PropertyIsError] != true || main.Properties[PropertyErrorMessage] != "boom" {
		t.Errorf("main properties = %#v", main.Properties)
	}
	if sibling.Event != EventException || sibling.DistinctID != "user-1" {
		t.Errorf("sibling identity = %#v", sibling)
	}
	if sibling.Properties[PropertyToolName] != "broken_tool" || sibling.Properties[PropertyClientUserAgent] != "fixture/1" {
		t.Errorf("sibling attribution = %#v", sibling.Properties)
	}
	if sibling.Properties[PropertyParameters] != nil {
		t.Errorf("sibling copied parameters: %#v", sibling.Properties[PropertyParameters])
	}
	list, ok := sibling.Properties["$exception_list"].([]posthog.ExceptionItem)
	if !ok || len(list) != 1 || list[0].Value != "boom" || list[0].Type != "Error" {
		t.Errorf("exception list = %#v", sibling.Properties["$exception_list"])
	}
}

func TestPanicFailurePreservesOriginalPanicValueForCaller(t *testing.T) {
	panicValue := &struct{ label string }{label: "original"}
	defer func() {
		if recovered := recover(); recovered != panicValue {
			t.Fatalf("recovered = %#v, want original panic value", recovered)
		}
	}()
	func() {
		defer func() {
			recovered := recover()
			failure := classifyPanic(recovered)
			if failure.errorType != "Panic" || failure.message == "" {
				t.Errorf("panic failure = %#v", failure)
			}
			panic(recovered)
		}()
		panic(panicValue)
	}()
}

type namedFixtureError struct{}

func (namedFixtureError) Error() string { return "underlying failure" }
