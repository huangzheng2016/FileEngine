// Package actor marks requests that did not arrive over HTTP. The desktop shell
// dispatches in-process calls with a context built by Go code, so a network
// client can never produce the marker.
package actor

import "context"

type key struct{}

// WithDesktop marks ctx as a trusted in-process call from the desktop shell.
func WithDesktop(ctx context.Context) context.Context {
	return context.WithValue(ctx, key{}, true)
}

// Desktop reports whether ctx carries the desktop marker.
func Desktop(ctx context.Context) bool {
	v, _ := ctx.Value(key{}).(bool)
	return v
}
