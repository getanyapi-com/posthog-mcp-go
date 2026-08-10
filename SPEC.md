# PostHog MCP Analytics for Go Specification

Status: implementation contract for `v0.1.0`.

## 1. Sources and precedence

The published PostHog event and privacy documentation is normative. The
TypeScript package at commit
`80f15a386621514c43f19e99ee4e3f702e4d369d` is the primary implementation
oracle because its server-wrapper model most closely matches the Go adapter.
Python at commit `6e5389ac8ef1a3e9d6ce6fa02d072d6d73be3fc5`
is an independent cross-check.

When the implementations disagree and the documentation is silent, Go follows
TypeScript and freezes the decision in a literal fixture. One known disagreement
is deterministic hashing of non-BMP Unicode: TypeScript hashes UTF-16 code units,
while Python hashes Unicode code points. Go follows TypeScript.

PipeOps' internal Go analytics implementation is non-normative and is not copied.

## 2. Supported dependencies

- Module: `github.com/getanyapi-com/posthog-mcp-go`
- Package: `posthogmcp`
- Go: 1.25 or newer, inherited from the MCP Go SDK
- MCP Go SDK: minimum `v1.6.1`, tested against current `v1.7.0`
- PostHog Go SDK: `v1.23.0`

Only public MCP Go SDK interfaces may be used. The adapter must not use
reflection or private SDK fields.

## 3. Public API

The package exports one instrumentation object:

```go
type Analytics struct { /* private */ }

type Options struct {
    Logger                      *slog.Logger
    Identify                    IdentifyFunc
    IntentFallback              IntentFallbackFunc
    EventProperties             EventPropertiesFunc
    BeforeSend                  BeforeSendFunc
    DisableContextInjection     bool
    ContextDescription          string
    ReportMissing               bool
    MissingCapabilityToolName   string
    EnableConversationID        bool
    DisableExceptionAutocapture bool
}

type Identity struct {
    DistinctID string
    Properties posthog.Properties
    Groups     posthog.Groups
}

type Event struct {
    UUID       string
    Event      string
    DistinctID string
    Timestamp  time.Time
    Properties posthog.Properties
    Groups     posthog.Groups
}

type IdentifyFunc func(context.Context, mcp.Request) (*Identity, error)
type IntentFallbackFunc func(context.Context, mcp.Request) (string, error)
type EventPropertiesFunc func(context.Context, mcp.Request) (posthog.Properties, error)
type BeforeSendFunc func(context.Context, Event) (*Event, error)

func New(posthog.EnqueueClient, *Options) *Analytics
func (*Analytics) Middleware() mcp.Middleware
func (*Analytics) Capture(context.Context, string, map[string]any) error
func (*Analytics) Enabled() bool
```

`New(nil, ...)` returns a disabled object. Disabled middleware is pass-through,
`Enabled` is false, and `Capture` is a no-op. A nil `Options` selects defaults.

The caller owns the PostHog client and calls `Close` or `CloseWithContext`.

## 4. Defaults

- Context schema injection: enabled.
- Exception autocapture: enabled.
- Conversation ID injection: disabled.
- Missing-capability tool: disabled.
- Missing-capability name: `get_more_tools`.
- Logger: no-op and safe for stdio transports.

The default context description is the exact upstream PostHog description.

## 5. MCP interception

`Analytics.Middleware` returns one `mcp.Middleware` for
`Server.AddReceivingMiddleware`. It observes:

- `initialize`
- `server/discover` as `$mcp_initialize` on MCP 2026-07-28 and newer
- `tools/list`
- `tools/call`
- `resources/list`
- `resources/read`
- `prompts/list`
- `prompts/get`

Tools registered before or after middleware attachment are covered because the
middleware wraps the central receiving handler.

The MCP Go SDK annotates `server/discover` results with server identity only
after receiving middleware returns. Therefore the canonical discovery event has
client and protocol attribution immediately; applications that require server
name/version on that event supply them through `EventProperties`.

For ordinary requests, downstream is called exactly once and its result and
error are preserved. Wrapper-owned changes are limited to enabled features:

- cloned `tools/list` schemas receive analytics-owned parameters;
- only analytics-owned arguments are stripped before typed validation;
- conversation instructions may be added to cloned tool results;
- the virtual missing-capability tool is handled by the wrapper.

If a schema cannot safely be extended, the original schema is returned and no
ownership is recorded. Existing customer fields named `context`,
`conversation_id`, or `_mcp_instructions` are never owned or stripped.

Adding the same `Analytics` middleware twice must not double-capture a request.
Distinct `Analytics` objects intentionally fan out to distinct clients.

## 6. Canonical events

The package exports constants for:

- `$mcp_initialize`
- `$mcp_tools_list`
- `$mcp_tool_call`
- `$mcp_resources_list`
- `$mcp_resource_read`
- `$mcp_prompts_list`
- `$mcp_prompt_get`
- `$mcp_missing_capability`
- `$identify`
- `$exception`
- `$mcp_custom`

Custom `Capture` sends the caller's event name verbatim, not `$mcp_custom`.

Canonical properties include server, client, protocol, session, conversation,
duration, tool/resource, listed tools, parameters, response, intent, error,
source, user-agent, and vendor-client fields defined by PostHog's event reference.
`$mcp_source` is always `posthog_mcp_analytics`. The transport SDK supplies
its standard `$lib` and `$lib_version` properties.

Anonymous events use the analytics session as `distinct_id` and set
`$process_person_profile` to false. Identified events use `Identity.DistinctID`,
`$set`, and `$groups`. `$identify` is emitted once per session and again only
when material identity changes. Identity state is bounded to 1,000 sessions,
matching upstream.

Tool failures include both returned protocol errors and `CallToolResult.IsError`.
A failed tool emits the normal `$mcp_tool_call` and, unless disabled, one
`$exception` sibling.

## 7. Event pipeline

Each event passes through this order:

1. snapshot request/session attribution;
2. resolve identity, metadata, and intent inside isolation guards;
3. build the canonical MCP event;
4. sanitize captured parameters and responses;
5. truncate values and the total event;
6. apply `EventProperties`;
7. build one main PostHog event and optional exception sibling;
8. invoke `BeforeSend` independently for each event;
9. enqueue each surviving `posthog.Capture`.

All mutable data given to hooks or PostHog is cloned. Hook mutation cannot alter
the MCP request, result, sibling event, or another concurrent capture.
`BeforeSend` is the trusted final wire mutation, matching the official wrappers:
JSON-compatible values it adds are cloned but are not re-redacted or re-truncated.

`Capture` called with an MCP handler context inherits that request's attribution.
Outside an MCP request it uses a generated anonymous session.

## 8. Privacy and size contract

Before `BeforeSend`, recursively:

- redact case-insensitive secret, token, credential, authorization, cookie,
  password, and API-key fields;
- redact PostHog keys embedded in strings;
- replace image, audio, blob, data-URL, and large base64 content;
- normalize JSON-compatible maps, slices, numbers, errors, and unknown values;
- detect cycles without panicking or mutating input.

Upstream limits are authoritative:

- maximum depth: 10;
- maximum breadth per collection: 100;
- maximum ordinary string: 32,768 bytes;
- maximum event JSON: 102,400 bytes;
- maximum intent and error message: 2,048 bytes;
- maximum resource/metadata string: 256 bytes;
- maximum exception frames: 50.

Total-size reduction follows the upstream progressive depth reduction and
largest-field truncation strategy. UTF-8 is never cut into invalid text.
The 100 KB budget applies to the event supplied to `BeforeSend`; PostHog may
reject a hook result that intentionally expands beyond its delivery limits.

## 9. Sessions and conversation IDs

Protocol session IDs are mapped to `ses_` plus two FNV-1a hashes. The algorithm
hashes UTF-16 code units to match TypeScript. Protocol-derived sessions do not
roll over. Generated sessions roll after 30 minutes of inactivity.

When enabled, a valid conversation ID takes precedence over a protocol session,
is deterministically mapped to a session, is stripped only when wrapper-owned,
and is returned through supported result channels. Conversation IDs are UUIDv7.

The TypeScript self-encoded legacy `Mcp-Session-Id` token is not implemented:
the Go SDK owns the stateful transport and exposes no response-header mutation
seam. This is an explicit parity deviation.

## 10. Failure isolation

Automatic analytics never returns an analytics error into MCP. Every
wrapper-owned hook, logger call, transformation, and `Enqueue` is protected from
errors and panics. Queue-full, closed-client, serialization, and delivery errors
drop only analytics.

Downstream panics are not swallowed. The wrapper may capture them best-effort,
then re-panics the original value.

No network call, extra delivery queue, or per-request goroutine is added.
`posthog.EnqueueClient.Enqueue` is the supported nonblocking delivery boundary.
Application hooks are synchronous and must return promptly; no library can make
an intentionally blocking callback fail-open without adding cancellation or
goroutine semantics that are outside this contract.

## 11. Compatibility deviations

- No protocol-neutral custom dispatcher API in `v0.1.0`.
- No TypeScript/Python private-handler compatibility machinery.
- No self-encoded legacy session token.
- `$lib` remains `posthog-go` because `posthog-go` overwrites that property.
  `$mcp_source` retains unambiguous MCP attribution.
- Go wrapper construction is explicit middleware rather than a global server
  registry. Duplicate attachment of the same object is request-deduplicated.
