package asyncop

import (
	"context"
	"testing"
)

func TestBeginSupersedesPrevious(t *testing.T) {
	var l Latest
	ctx1, gen1 := l.Begin(context.Background())
	ctx2, gen2 := l.Begin(context.Background())

	if ctx1.Err() == nil {
		t.Error("first operation was not cancelled by the second Begin")
	}
	if ctx2.Err() != nil {
		t.Error("newest operation is cancelled")
	}
	if l.IsCurrent(gen1) {
		t.Error("superseded generation still reports current")
	}
	if !l.IsCurrent(gen2) {
		t.Error("newest generation does not report current")
	}
}

func TestCancelMakesEverythingStale(t *testing.T) {
	var l Latest
	ctx, gen := l.Begin(context.Background())
	l.Cancel()

	if ctx.Err() == nil {
		t.Error("Cancel did not cancel the context")
	}
	if l.IsCurrent(gen) {
		t.Error("generation still current after Cancel")
	}
	// Cancel with nothing running must not panic.
	l.Cancel()
}

func TestParentCancellationPropagates(t *testing.T) {
	var l Latest
	parent, cancel := context.WithCancel(context.Background())
	ctx, _ := l.Begin(parent)
	cancel()
	if ctx.Err() == nil {
		t.Error("parent cancellation did not reach the operation")
	}
}
