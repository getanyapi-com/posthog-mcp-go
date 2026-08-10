# Contributor instructions

`SPEC.md` is the behavioral contract. Tests observe behavior through the public
`Analytics` API, real official MCP Go SDK transports, and the external
`posthog.EnqueueClient` boundary.

- Use red-green TDD in vertical slices.
- Use only public `modelcontextprotocol/go-sdk` APIs.
- Keep every non-test Go source file at or below 300 lines excluding blank lines
  and comments.
- Automatic analytics errors and panics must never escape into MCP.
- Do not add a delivery queue, request goroutine, reflection, or private SDK access.
- Preserve source attribution for behavior or fixtures ported from PostHog.
- Run `gofmt`, `go vet ./...`, `go test ./...`, and `go test -race ./...` before handoff.
