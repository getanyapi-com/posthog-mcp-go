package posthogmcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type middlewareVisit struct {
	analytics *Analytics
	parent    *middlewareVisit
}

type middlewareVisitKey struct{}

// Middleware returns receiving middleware for mcp.Server.AddReceivingMiddleware.
func (a *Analytics) Middleware() mcp.Middleware {
	state := newMiddlewareState()
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (result mcp.Result, err error) {
			if !a.Enabled() || middlewareVisited(ctx, a) {
				return next(ctx, method, request)
			}
			ctx = context.WithValue(ctx, middlewareVisitKey{}, &middlewareVisit{analytics: a, parent: middlewareVisits(ctx)})
			started := time.Now()

			if method == "tools/call" {
				return a.handleToolCall(ctx, state, next, request, started)
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					a.emitLifecycle(ctx, lifecycleSnapshot{method: method, request: request, err: fmt.Errorf("panic: %v", recovered), started: started})
					panic(recovered)
				}
				a.emitLifecycle(ctx, lifecycleSnapshot{method: method, request: request, result: result, err: err, started: started})
			}()
			result, err = next(ctx, method, request)
			if err == nil && method == "tools/list" {
				result = a.prepareToolsList(state, result)
			}
			return result, err
		}
	}
}

func middlewareVisits(ctx context.Context) *middlewareVisit {
	visits, _ := ctx.Value(middlewareVisitKey{}).(*middlewareVisit)
	return visits
}

func middlewareVisited(ctx context.Context, analytics *Analytics) bool {
	for visit := middlewareVisits(ctx); visit != nil; visit = visit.parent {
		if visit.analytics == analytics {
			return true
		}
	}
	return false
}
