package posthogmcp

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func cloneRequestForCallback(request mcp.Request) (cloned mcp.Request) {
	defer func() {
		if recover() != nil {
			cloned = nil
		}
	}()
	switch request := request.(type) {
	case *mcp.ServerRequest[*mcp.InitializeParams]:
		return cloneServerRequest(request, &mcp.InitializeParams{})
	case *mcp.ServerRequest[*mcp.CallToolParamsRaw]:
		return cloneServerRequest(request, &mcp.CallToolParamsRaw{})
	case *mcp.ServerRequest[*mcp.ListToolsParams]:
		return cloneServerRequest(request, &mcp.ListToolsParams{})
	case *mcp.ServerRequest[*mcp.ListResourcesParams]:
		return cloneServerRequest(request, &mcp.ListResourcesParams{})
	case *mcp.ServerRequest[*mcp.ReadResourceParams]:
		return cloneServerRequest(request, &mcp.ReadResourceParams{})
	case *mcp.ServerRequest[*mcp.ListPromptsParams]:
		return cloneServerRequest(request, &mcp.ListPromptsParams{})
	case *mcp.ServerRequest[*mcp.GetPromptParams]:
		return cloneServerRequest(request, &mcp.GetPromptParams{})
	default:
		return nil
	}
}

func cloneServerRequest[P mcp.Params](source *mcp.ServerRequest[P], params P) mcp.Request {
	if source == nil {
		return nil
	}
	wire, err := json.Marshal(source.Params)
	if err != nil || json.Unmarshal(wire, params) != nil {
		return nil
	}
	return &mcp.ServerRequest[P]{Session: source.Session, Params: params, Extra: cloneRequestExtra(source.Extra)}
}

func cloneRequestExtra(source *mcp.RequestExtra) *mcp.RequestExtra {
	if source == nil {
		return nil
	}
	clone := *source
	clone.Header = source.Header.Clone()
	if source.TokenInfo != nil {
		token := *source.TokenInfo
		token.Scopes = append([]string(nil), source.TokenInfo.Scopes...)
		token.Extra = cloneJSONMap(source.TokenInfo.Extra)
		clone.TokenInfo = &token
	}
	return &clone
}

func cloneJSONMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	wire, err := json.Marshal(source)
	if err != nil {
		return nil
	}
	var clone map[string]any
	if json.Unmarshal(wire, &clone) != nil {
		return nil
	}
	return clone
}
