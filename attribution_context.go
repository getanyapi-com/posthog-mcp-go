package posthogmcp

import "context"

type attributionContextKey struct{}

func withRequestAttribution(ctx context.Context, attribution requestAttribution) context.Context {
	snapshot := attribution
	snapshot.identity = cloneIdentity(attribution.identity)
	return context.WithValue(ctx, attributionContextKey{}, snapshot)
}

func requestAttributionFromContext(ctx context.Context) (requestAttribution, bool) {
	if ctx == nil {
		return requestAttribution{}, false
	}
	attribution, ok := ctx.Value(attributionContextKey{}).(requestAttribution)
	if !ok {
		return requestAttribution{}, false
	}
	attribution.identity = cloneIdentity(attribution.identity)
	return attribution, true
}
