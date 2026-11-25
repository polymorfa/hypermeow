package store

import "context"

type statusSessionContextKey struct{}

// WithStatusSession isolates pairwise status sessions from ordinary chat sessions.
// Identity, prekey and sender-key stores remain shared. The suffix follows the
// device address so SQL user-prefix migration and deletion cover both scopes.
func WithStatusSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, statusSessionContextKey{}, true)
}

// SignalSessionAddress returns the storage key in the selected pairwise scope.
func SignalSessionAddress(ctx context.Context, address string) string {
	if scoped, _ := ctx.Value(statusSessionContextKey{}).(bool); scoped {
		return address + "_status"
	}
	return address
}
