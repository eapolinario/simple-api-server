/*
Copyright 2026 Eduardo Apolinario.
*/

package task

import (
	"context"
	"strconv"

	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
)

// requestNamespace returns the namespace from the request context. The
// second return is false if no namespace info is in the context (e.g.,
// a cluster-wide list).
func requestNamespace(ctx context.Context) (string, bool) {
	return genericapirequest.NamespaceFrom(ctx)
}

// parseRVOrZero parses a stamped resourceVersion. Returns 0 on parse
// failure rather than propagating the error: the store always stamps
// well-formed values, so anything else is a programmer bug and the
// list-RV high-water-mark fallback below covers it.
func parseRVOrZero(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// formatRV renders a numeric resourceVersion in the canonical string form.
func formatRV(rv uint64) string {
	return strconv.FormatUint(rv, 10)
}
