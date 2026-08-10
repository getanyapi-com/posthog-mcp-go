# posthog-mcp-go

Community-maintained PostHog MCP analytics for the official
[`modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk).

This project is maintained by [AnyAPI](https://getanyapi.com). It is not an
official PostHog package. Its behavior is specified in [SPEC.md](SPEC.md) and
tested against pinned versions of PostHog's official TypeScript and Python MCP
analytics implementations.

## Install

```bash
go get github.com/getanyapi-com/posthog-mcp-go@v0.1.0
```

Go 1.25 or newer is required. MCP Go SDK v1.6.1 and v1.7.0 are tested in CI.

## Use

```go
package main

import (
	"context"
	"log"

	posthogmcp "github.com/getanyapi-com/posthog-mcp-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/posthog/posthog-go"
)

func instrument(server *mcp.Server, projectKey string) posthog.Client {
	client, err := posthog.NewWithConfig(projectKey, posthog.Config{
		Endpoint: posthog.DefaultEndpoint,
	})
	if err != nil {
		log.Printf("PostHog analytics disabled: %v", err)
		return nil
	}

	analytics := posthogmcp.New(client, &posthogmcp.Options{
		Identify: func(context.Context, mcp.Request) (*posthogmcp.Identity, error) {
			return &posthogmcp.Identity{DistinctID: "customer_123"}, nil
		},
	})
	server.AddReceivingMiddleware(analytics.Middleware())
	return client
}
```

The application owns the PostHog client. Call `Close` or `CloseWithContext`
during shutdown so queued events can flush.

Context schema injection and exception autocapture are enabled by default.
Conversation IDs and missing-capability reporting are opt-in. See `Options` in
the [package API](https://pkg.go.dev/github.com/getanyapi-com/posthog-mcp-go).

Custom events use the same privacy and delivery pipeline:

```go
_ = analytics.Capture(ctx, "feedback_submitted", map[string]any{"rating": 5})
```

## Failure model

Instrumentation is pass-through when the client is nil. Callback, logger, data
conversion, queue-full, closed-client, and enqueue failures drop only analytics.
The middleware calls ordinary downstream handlers exactly once and preserves
their result, error, or panic. It adds no request goroutine, delivery queue, or
network call; `posthog-go` owns asynchronous delivery.

## Compatibility and parity

The package emits PostHog's canonical MCP events, sanitizes sensitive and binary
payloads, enforces upstream size limits, and supports legacy `initialize` plus
the MCP 2026-07-28 `server/discover` handshake. Intent context is injected only
when the tool schema can be cloned safely, and wrapper-owned arguments are
removed before typed tool validation.

Documented parity decisions and test provenance are in [SPEC.md](SPEC.md) and
[TEST-PORT-MATRIX.md](TEST-PORT-MATRIX.md). The TypeScript legacy self-encoded
session header is intentionally absent because the Go SDK owns that transport
state. `$lib` remains `posthog-go`; `$mcp_source` identifies this integration.
