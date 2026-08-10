package posthogmcp

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructionsKey = "_mcp_instructions"

const conversationPrompt = "[SERVER]: Reuse conversation_id=%s on every subsequent tool call in this conversation. Required for the server to correlate calls and provide context-aware results."

var conversationIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type conversationResolution struct {
	id     string
	minted bool
}

func resolveConversationID(enabled bool, arguments map[string]any) conversationResolution {
	if !enabled {
		return conversationResolution{}
	}
	if supplied, ok := arguments["conversation_id"].(string); ok {
		supplied = strings.TrimSpace(supplied)
		if conversationIDPattern.MatchString(supplied) {
			return conversationResolution{id: strings.ToLower(supplied)}
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	return conversationResolution{id: id.String(), minted: true}
}

func injectOutputInstructions(input any) (any, bool) {
	schema, ok := cloneSchemaObject(input, false)
	if !ok || schema["$ref"] != nil || schema["oneOf"] != nil || schema["allOf"] != nil || schema["anyOf"] != nil {
		return input, false
	}
	properties, ok := schemaProperties(schema)
	if !ok {
		return input, false
	}
	if _, exists := properties[instructionsKey]; exists {
		return input, false
	}
	properties[instructionsKey] = map[string]any{
		"type":        "object",
		"description": "Server-issued handles for this conversation, and what to do with them. Read and follow.",
		"properties": map[string]any{
			"conversation_id": map[string]any{
				"type":        "string",
				"description": "Echo this exact value as the conversation_id argument on every subsequent tool call.",
			},
			"instructions": map[string]any{"type": "string"},
		},
	}
	schema["properties"] = properties
	return schema, true
}

func applyConversationResult(result mcp.Result, conversation conversationResolution, ownership parameterOwnership) mcp.Result {
	if conversation.id == "" {
		return result
	}
	toolResult, ok := result.(*mcp.CallToolResult)
	if !ok || toolResult == nil {
		return result
	}
	clone := *toolResult
	delivered := false
	if ownership.outputInstructions {
		if structured, ok := cloneStringMap(toolResult.StructuredContent); ok {
			if _, exists := structured[instructionsKey]; !exists {
				structured[instructionsKey] = map[string]any{
					"conversation_id": conversation.id,
					"instructions":    "Send this conversation_id as an argument on every subsequent tool call in this conversation.",
				}
				clone.StructuredContent = structured
				delivered = true
			}
		}
	}
	if conversation.minted {
		clone.Content = append(append([]mcp.Content(nil), toolResult.Content...), &mcp.TextContent{Text: formatConversationPrompt(conversation.id)})
		delivered = true
	}
	if !delivered && conversation.minted {
		return result
	}
	return &clone
}

func cloneStringMap(input any) (map[string]any, bool) {
	return cloneSchemaObject(input, false)
}

func formatConversationPrompt(id string) string {
	return strings.Replace(conversationPrompt, "%s", id, 1)
}
