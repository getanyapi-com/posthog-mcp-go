# Upstream Test Port Matrix

Pinned sources:

- TypeScript: `PostHog/posthog-js@80f15a386621514c43f19e99ee4e3f702e4d369d`
- Python: `PostHog/posthog-python@6e5389ac8ef1a3e9d6ce6fa02d072d6d73be3fc5`

Statuses are updated as vertical slices land.

## TypeScript

| Upstream test | Disposition |
| --- | --- |
| `basic-server` | Port through real Go SDK transports. |
| `beforeSend` | Port event mutation, drop, error, and panic behavior. |
| `capture` | Port through public `Analytics.Capture`. |
| `client-identity` | Port request/session client attribution. |
| `compatibility` | Not applicable: Go targets one public SDK interface. |
| `concurrent-attribution` | Port under the race detector. |
| `context-parameters` | Port schema injection fixtures. |
| `context-preservation` | Port customer-owned argument behavior. |
| `conversation-id` | Port as opt-in middleware behavior. |
| `conversation-session-id` | Port deterministic correlation fixtures. |
| `e2e-sanitization` | Port literal request/result fixtures. |
| `e2e-truncation` | Port literal size fixtures. |
| `edge-runtime-compatibility` | Not applicable to Go. |
| `error-capture` | Port result and protocol error paths. |
| `exceptions` | Port canonical exception sibling behavior. |
| `handler-property` | Not applicable: no private handler patching. |
| `identify` | Port identity and `$identify` behavior. |
| `identity-cache` | Port bounded deduplication behavior. |
| `ids` | Port UUIDv7 shape and literal deterministic vectors. |
| `instrument-lowlevel` | Covered through `mcp.Middleware`. |
| `instrument-mutator` | Not applicable: no framework mutator API. |
| `late-handler-registration` | Port late tool registration. |
| `lib-identity` | Documented deviation: `$lib` remains `posthog-go`. |
| `logger-isolation` | Port panicking logger behavior. |
| `mcp-payloads` | Port sanitization fixtures. |
| `mcp-version-compatibility` | Port dependency-version CI matrix. |
| `output-instructions` | Port conversation result behavior. |
| `posthog-events` | Port every canonical event/property fixture. |
| `posthog-mcp` | Not applicable: no manual dispatcher in v0. |
| `report-missing` | Port opt-in virtual tool behavior. |
| `reserved-arguments` | Port ownership and stripping behavior. |
| `sanitization` | Port literal pure fixtures through public capture. |
| `sdk-import-boundary` | Port as a compile/import test. |
| `session-id` | Port deterministic and rollover behavior. |
| `session-token` | Explicit deviation: no Go SDK response-header seam. |
| `sink` | Port enqueue error and panic behavior. |
| `stateless-session` | Covered by opt-in conversation IDs. |
| `string-method-registration` | Not applicable to typed Go registration. |
| `tool-categories` | Port `_meta.category` capture. |
| `tracing-initialization` | Port initialize timing and metadata. |
| `transport-identity` | Port HTTP headers through Streamable HTTP. |
| `truncation` | Port limits and progressive event reduction. |

## Python

| Upstream module | Disposition |
| --- | --- |
| `test_fastmcp` | Covered through real Go SDK middleware tests. |
| `test_fastmcp_v2` | Covered through current Go SDK compatibility tests. |
| `test_features_m4` | Port context, conversation, and missing-capability behavior. |
| `test_instrumentation_fork` | Not applicable: Go owns no analytics worker/task pool. |
| `test_lowlevel` | Covered through real Go SDK middleware tests. |
| `test_pending_tasks` | Not applicable: no wrapper-owned background tasks. |
| `test_pipeline` | Port canonical event, ID, privacy, and pipeline fixtures. |
| `test_posthog_mcp` | Not applicable: no manual dispatcher in v0. |
| `test_review_fixes` | Review each regression and port applicable public behavior. |
| `test_session_token` | Explicit deviation: no Go SDK response-header seam. |
| `test_truncation` | Port literal limits and total-budget behavior. |
| `test_units` | Port behavior reachable through the public API. |
