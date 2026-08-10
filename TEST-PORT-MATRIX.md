# Upstream Test Port Matrix

Pinned sources:

- TypeScript: `PostHog/posthog-js@80f15a386621514c43f19e99ee4e3f702e4d369d`
- Python: `PostHog/posthog-python@6e5389ac8ef1a3e9d6ce6fa02d072d6d73be3fc5`

Statuses are updated as vertical slices land.

## TypeScript

| Upstream test | Disposition |
| --- | --- |
| `basic-server` | Ported through in-memory and Streamable HTTP Go SDK transports. |
| `beforeSend` | Ported through `Analytics.Capture`: mutation, drop, error, and panic. |
| `capture` | Ported through public `Analytics.Capture`. |
| `client-identity` | Ported client, protocol, session, header, and per-request metadata attribution. |
| `compatibility` | Not applicable: Go targets one public SDK interface. |
| `concurrent-attribution` | Ported across distinct sessions under the race detector. |
| `context-parameters` | Ported schema injection, intent source, and stripping fixtures. |
| `context-preservation` | Ported customer-owned argument behavior. |
| `conversation-id` | Ported as opt-in middleware behavior with UUIDv7 fallback coverage. |
| `conversation-session-id` | Ported deterministic correlation fixtures. |
| `e2e-sanitization` | Ported literal request/result and sensitive-key fixtures. |
| `e2e-truncation` | Ported literal field and event-budget fixtures. |
| `edge-runtime-compatibility` | Not applicable to Go. |
| `error-capture` | Ported returned errors, protocol errors, `IsError`, and panics. |
| `exceptions` | Ported canonical exception sibling behavior and opt-out. |
| `handler-property` | Not applicable: no private handler patching. |
| `identify` | Ported identity, anonymous merge, mutation isolation, and `$identify` behavior. |
| `identity-cache` | Ported bounded LRU and material-change deduplication behavior. |
| `ids` | Ported UUIDv7 shape and literal deterministic TypeScript vectors. |
| `instrument-lowlevel` | Covered through `mcp.Middleware`. |
| `instrument-mutator` | Not applicable: no framework mutator API. |
| `late-handler-registration` | Ported by registering tools after central middleware attachment and after a virtual same-name tool was listed and called. |
| `lib-identity` | Documented deviation: `$lib` remains `posthog-go`. |
| `logger-isolation` | Ported with a panicking `slog.Handler`. |
| `mcp-payloads` | Ported exact request envelopes, owned-field exclusion, and sanitization fixtures. |
| `mcp-version-compatibility` | Ported in CI against v1.6.1 and v1.7.0. |
| `output-instructions` | Ported conversation text and structured result behavior. |
| `posthog-events` | Ported canonical lifecycle, identify, exception, attribution, and envelope fixtures. |
| `posthog-mcp` | Not applicable: no manual dispatcher in v0. |
| `report-missing` | Ported opt-in virtual tool, direct-call, exact SDK error recognition, and real-name collision behavior. |
| `reserved-arguments` | Ported ownership, customer-field preservation, and stripping behavior. |
| `sanitization` | Ported literal privacy vectors through public capture. |
| `sdk-import-boundary` | Ported through the external-package public API test. |
| `session-id` | Ported deterministic, per-connection, conversation, and rollover behavior. |
| `session-token` | Explicit deviation: no Go SDK response-header seam. |
| `sink` | Ported enqueue error and panic behavior through public capture. |
| `stateless-session` | Covered by opt-in conversation IDs. |
| `string-method-registration` | Not applicable to typed Go registration. |
| `tool-categories` | Ported description and `_meta.category` capture on tool and exception events. |
| `tracing-initialization` | Ported legacy initialize and current server/discover timing and metadata. |
| `transport-identity` | Ported HTTP headers through Streamable HTTP. |
| `truncation` | Ported field, recursive, and total-event limits through public capture. |

## Python

| Upstream module | Disposition |
| --- | --- |
| `test_fastmcp` | Covered through real Go SDK middleware tests. |
| `test_fastmcp_v2` | Covered through current Go SDK compatibility tests. |
| `test_features_m4` | Ported context, conversation, and missing-capability behavior. |
| `test_instrumentation_fork` | Not applicable: Go owns no analytics worker/task pool. |
| `test_lowlevel` | Covered through real Go SDK middleware tests. |
| `test_pending_tasks` | Not applicable: no wrapper-owned background tasks. |
| `test_pipeline` | Ported privacy, custom capture, canonical automatic event, and ID fixtures. |
| `test_posthog_mcp` | Not applicable: no manual dispatcher in v0. |
| `test_review_fixes` | Ported applicable hook isolation, malformed value, collision, and fail-open regressions. |
| `test_session_token` | Explicit deviation: no Go SDK response-header seam. |
| `test_truncation` | Ported literal limits and total-budget behavior. |
| `test_units` | Ported behavior reachable through the public API. |
