// Package asyncop keeps only the newest of a series of async operations.
//
// Search-as-you-type and pickers start a request per keystroke. Without
// coordination a slow early request ("c") can finish after a fast later one
// ("cat") and overwrite its results. Latest cancels the previous operation
// when a new one begins and lets the result callback check whether it is
// still the newest before touching the UI.
package asyncop

import (
	"context"
	"sync"
)

// Latest tracks the newest operation. The zero value is ready to use.
type Latest struct {
	mu     sync.Mutex
	gen    uint64
	cancel context.CancelFunc
}

// Begin cancels the previous operation and starts a new one, returning its
// context and generation. The context is cancelled when the next Begin or
// Cancel is called, or when parent is.
func (l *Latest) Begin(parent context.Context) (context.Context, uint64) {
	ctx, cancel := context.WithCancel(parent)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cancel != nil {
		l.cancel()
	}
	l.gen++
	l.cancel = cancel
	return ctx, l.gen
}

// IsCurrent reports whether gen is still the newest operation, so its result
// may be applied.
func (l *Latest) IsCurrent(gen uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return gen == l.gen
}

// Cancel cancels the current operation, if any, and makes every outstanding
// generation stale.
func (l *Latest) Cancel() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
	l.gen++
}
