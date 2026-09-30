package service

import (
	"context"

	"github.com/mmedum/google-docs-mcp/v2/internal/render"
)

// Asker puts a question to the person using the server before a write
// that cannot be undone through this server, or that destroys other
// people's work in a document (§12a).
// Ask returns nil when the write may go ahead, and an error to return in
// its place otherwise; the tools layer installs one per call, for the
// tools that ask.
type Asker interface {
	Ask(ctx context.Context, q render.Question) error
	// Asks reports whether Ask would put a question or refuse, rather
	// than let the write go ahead unasked: the client can ask, or the
	// configuration requires it.
	Asks() bool
}

type askerKey struct{}

// WithAsker returns a context whose asking writes are put to a.
func WithAsker(ctx context.Context, a Asker) context.Context {
	return context.WithValue(ctx, askerKey{}, a)
}

// ask is the last step before an asking write, after every read, every
// other guard and the dry run, so the question shows what the write
// would do and nothing is asked that a guard would refuse anyway. A
// write reached with no asker is refused: only a tool registered to ask
// may make one.
func ask(ctx context.Context, q render.Question) error {
	a, ok := ctx.Value(askerKey{}).(Asker)
	if !ok {
		return Errorf("unexpected", "this write has no way to ask the person, which is a defect in this "+
			"server; nothing was changed")
	}
	return a.Ask(ctx, q)
}

// asks reports whether an asking write on ctx would reach the person,
// so a write pays for a read that only its question shows when it does.
func asks(ctx context.Context) bool {
	a, ok := ctx.Value(askerKey{}).(Asker)
	return !ok || a.Asks()
}
