package posthogmcp

import "github.com/modelcontextprotocol/go-sdk/mcp"

const missingCapabilityDescription = "Check for additional tools whenever your task might benefit from specialized capabilities - even if existing tools could work as a fallback."

const missingCapabilityContextDescription = "A description of your goal and what kind of tool would help accomplish it."

const missingCapabilityResponse = "Unfortunately, we have shown you the full tool list. We have noted your feedback and will work to improve the tool list in the future."

func missingCapabilityTool(name string) *mcp.Tool {
	destructive := false
	openWorld := true
	return &mcp.Tool{
		Name:        name,
		Description: missingCapabilityDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"context": map[string]any{"type": "string", "description": missingCapabilityContextDescription},
			},
			"required": []any{"context"},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:           "Get More Tools",
			ReadOnlyHint:    true,
			OpenWorldHint:   &openWorld,
			IdempotentHint:  true,
			DestructiveHint: &destructive,
		},
	}
}

func missingCapabilityResult() *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: missingCapabilityResponse}}}
}
