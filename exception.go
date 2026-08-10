package posthogmcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

type toolFailure struct {
	errorType string
	message   string
	panic     bool
}

func classifyToolFailure(result mcp.Result, returned error) *toolFailure {
	if returned != nil {
		return failureFromError(returned)
	}
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok || toolResult == nil || !toolResult.IsError {
		return nil
	}
	if underlying := toolResult.GetError(); underlying != nil {
		return failureFromError(underlying)
	}
	return &toolFailure{errorType: "Error", message: toolResultErrorMessage(toolResult)}
}

func classifyPanic(value any) *toolFailure {
	return &toolFailure{errorType: "Panic", message: safeValueString(value), panic: true}
}

func failureFromError(err error) *toolFailure {
	return &toolFailure{errorType: "Error", message: safeErrorString(err)}
}

func toolResultErrorMessage(result *mcp.CallToolResult) string {
	texts := make([]string, 0, len(result.Content))
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && text != nil {
			texts = append(texts, text.Text)
		}
	}
	message := strings.TrimSpace(strings.Join(texts, " "))
	if message == "" {
		return "Unknown error"
	}
	return message
}

func applyToolFailure(main *Event, failure *toolFailure) *Event {
	if main.Properties == nil {
		main.Properties = make(posthog.Properties)
	}
	if failure == nil {
		main.Properties[PropertyIsError] = false
		return nil
	}
	main.Properties[PropertyIsError] = true
	main.Properties[PropertyErrorType] = failure.errorType
	main.Properties[PropertyErrorMessage] = failure.message

	properties := make(posthog.Properties)
	for _, key := range exceptionAttributionProperties {
		if value, ok := main.Properties[key]; ok {
			properties[key] = value
		}
	}
	properties["$exception_level"] = "error"
	properties["$exception_list"] = buildExceptionList(failure)
	return &Event{
		UUID:       newPrefixedID("evt")[4:],
		Event:      EventException,
		DistinctID: main.DistinctID,
		Timestamp:  main.Timestamp,
		Properties: properties,
		Groups:     cloneGroups(main.Groups),
	}
}

var exceptionAttributionProperties = []string{
	PropertySessionID,
	PropertyConversationID,
	PropertyToolName,
	PropertyToolDescription,
	PropertyToolCategory,
	PropertyResourceName,
	PropertyServerName,
	PropertyServerVersion,
	PropertyClientName,
	PropertyClientVersion,
	PropertyClientUserAgent,
	PropertyVendorClient,
	PropertyProtocolVersion,
	"$process_person_profile",
}

func buildExceptionList(failure *toolFailure) []posthog.ExceptionItem {
	exception := posthog.NewDefaultException(
		time.Time{},
		"ignored",
		failure.errorType,
		failure.message,
	)
	handled := !failure.panic
	synthetic := !failure.panic
	exception.ExceptionList[0].Mechanism = &posthog.ExceptionMechanism{
		Handled:   &handled,
		Synthetic: &synthetic,
	}
	return exception.ExceptionList
}

func safeErrorString(err error) (message string) {
	defer func() {
		if recover() != nil {
			message = "Unknown error"
		}
	}()
	return err.Error()
}

func safeValueString(value any) (message string) {
	defer func() {
		if recover() != nil {
			message = "Unknown error"
		}
	}()
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprint(value)
}
