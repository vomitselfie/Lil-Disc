package mediaqueue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diamondburned/gotk4/pkg/glib/v2"
)

// pump drives the default GLib main context until done is closed, so tasks
// scheduled via glib.IdleAdd/TimeoutAdd (as Run and its callers do) actually
// get to run — nothing else is pumping a main loop in a plain `go test`.
func pump(t *testing.T, timeout time.Duration, done <-chan struct{}) {
	t.Helper()
	ctx := glib.MainContextDefault()
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-done:
			return
		default:
		}
		ctx.Iteration(false)
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the queue to drain")
		}
	}
}

// TestRunsOneAtATimeInOrder is the whole point of this package: whatever
// Run is given must never run concurrently with another queued task, and
// must run in the order queued, matching a FIFO of pipeline constructions.
func TestRunsOneAtATimeInOrder(t *testing.T) {
	const n = 8

	var (
		mu        sync.Mutex
		order     []int
		active    int
		maxActive int
	)
	done := make(chan struct{})
	var remaining int32 = n

	for i := 0; i < n; i++ {
		i := i
		Run(func(taskDone func()) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			order = append(order, i)
			mu.Unlock()

			// A task that "finishes" asynchronously, like a real pipeline
			// waiting on its "prepared" property — if Run let the next
			// task start before this fires, active would exceed 1.
			glib.TimeoutAdd(5, func() {
				mu.Lock()
				active--
				mu.Unlock()

				taskDone()
				if atomic.AddInt32(&remaining, -1) == 0 {
					close(done)
				}
			})
		})
	}

	pump(t, 5*time.Second, done)

	if maxActive != 1 {
		t.Errorf("max concurrently active tasks = %d, want 1", maxActive)
	}
	if len(order) != n {
		t.Fatalf("ran %d tasks, want %d", len(order), n)
	}
	for i, v := range order {
		if v != i {
			t.Errorf("order[%d] = %d, want %d (queue is not FIFO)", i, v, i)
			break
		}
	}
}

// TestQueueDrainsAndRestarts checks that a second batch queued after the
// first has fully drained (busy reset to false) still runs, i.e. the queue
// doesn't wedge itself once empty.
func TestQueueDrainsAndRestarts(t *testing.T) {
	run := func(n int) {
		t.Helper()
		done := make(chan struct{})
		var remaining int32 = int32(n)
		for i := 0; i < n; i++ {
			Run(func(taskDone func()) {
				taskDone()
				if atomic.AddInt32(&remaining, -1) == 0 {
					close(done)
				}
			})
		}
		pump(t, 5*time.Second, done)
	}

	run(3)
	run(3)
}

// TestTaskThatNeverCallsDoneOnlyStallsItself confirms a caller-side timeout
// (as both call sites use) is what has to unstick a hung task — Run itself
// has no timeout of its own, by design, since it doesn't know how long a
// real pipeline should reasonably take.
func TestTaskThatNeverCallsDoneOnlyStallsItself(t *testing.T) {
	ran := make(chan struct{})

	Run(func(done func()) {
		// Deliberately never calls done(): simulates a task whose own
		// safety-net timeout hasn't fired yet.
		_ = done
	})
	Run(func(done func()) {
		close(ran)
		done()
	})

	select {
	case <-ran:
		t.Fatal("second task ran although the first never called done")
	case <-time.After(200 * time.Millisecond):
	}
}
