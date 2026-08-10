package posthogmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Middleware returns receiving middleware for mcp.Server.AddReceivingMiddleware.
func (a *Analytics) Middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			return next(ctx, method, request)
		}
	}
}
