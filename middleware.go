package posthogmcp

import (
	"context"
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
			originalContext := ctx
			downstreamStarted := false
			downstreamFinished := false
			var downstreamResult mcp.Result
			var downstreamError error
			guardedNext := func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
				downstreamStarted = true
				downstreamResult, downstreamError = next(ctx, method, request)
				downstreamFinished = true
				return downstreamResult, downstreamError
			}
			defer func() {
				if recovered := recover(); recovered != nil {
					if downstreamStarted && !downstreamFinished {
						panic(recovered)
					}
					a.safeLog("analytics middleware preparation failed", "error", recovered)
					if downstreamFinished {
						result, err = downstreamResult, downstreamError
					} else {
						result, err = next(originalContext, method, request)
					}
				}
			}()
			if lifecycleEventName(method, false) == "" {
				return guardedNext(ctx, method, request)
			}
			ctx = context.WithValue(ctx, middlewareVisitKey{}, &middlewareVisit{analytics: a, parent: middlewareVisits(ctx)})
			started := time.Now()

			if method == "tools/call" {
				return a.handleToolCall(ctx, state, guardedNext, request, started)
			}
			attribution := a.resolveAttribution(request, "")
			identify := a.resolveIdentity(ctx, request, &attribution)
			ctx = withRequestAttribution(ctx, attribution)
			defer func() {
				if recovered := recover(); recovered != nil {
					a.emitLifecycle(ctx, lifecycleSnapshot{method: method, request: request, started: started, attribution: attribution, identify: identify, failure: classifyPanic(recovered)})
					panic(recovered)
				}
				if method == "initialize" {
					initialize, _ := result.(*mcp.InitializeResult)
					a.rememberInitialize(&attribution, initialize)
				}
				a.emitLifecycle(ctx, lifecycleSnapshot{method: method, request: request, result: result, err: err, started: started, attribution: attribution, identify: identify})
			}()
			result, err = guardedNext(ctx, method, request)
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
