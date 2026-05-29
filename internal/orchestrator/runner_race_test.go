package orchestrator

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/hecatehq/hecate/internal/taskstate"
)

// newTestRunner builds a minimal Runner suitable for race tests — no workers,
// no metrics, no executors needed.
func newTestRunner() *Runner {
	runner := &Runner{
		logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		store:    taskstate.NewMemoryStore(),
		policies: make(map[string]struct{}),
	}
	attachTestQueueCoordinator(runner, nil)
	return runner
}

// TestRunnerSetQueueRace is a regression test for the data race between
// SetQueue (writer) and RuntimeStats (reader) on the r.queue field.
func TestRunnerSetQueueRace(t *testing.T) {
	t.Parallel()
	runner := newTestRunner()

	q1 := NewMemoryRunQueue(4, time.Second)
	q2 := NewMemoryRunQueue(4, time.Second)

	var wg sync.WaitGroup
	const n = 10

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if (i+j)%2 == 0 {
					runner.SetQueue(q1)
				} else {
					runner.SetQueue(q2)
				}
			}
		}(i)
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = runner.RuntimeStats(context.Background())
			}
		}()
	}

	wg.Wait()
}

// TestRunnerSetQueueConcurrentWithWorker is a regression test for the data
// race between SetQueue and the background processQueue goroutines that
// NewRunner starts. Those goroutines read r.queue on every iteration.
func TestRunnerSetQueueConcurrentWithWorker(t *testing.T) {
	t.Parallel()
	// NewRunner launches background processQueue goroutines immediately.
	runner := NewRunner(
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		taskstate.NewMemoryStore(),
		nil,
		Config{QueueWorkers: 2},
	)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner.SetQueue(NewMemoryRunQueue(4, time.Second))
		}()
	}
	wg.Wait()
}

// TestRunnerEnqueueRunConcurrentWithSetQueue verifies that swapping the queue
// pointer while another goroutine calls enqueueRun doesn't produce a data
// race. enqueueRun reads r.queue to call Enqueue; errors are expected and
// intentionally ignored here.
func TestRunnerEnqueueRunConcurrentWithSetQueue(t *testing.T) {
	t.Parallel()
	runner := newTestRunner()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			runner.SetQueue(NewMemoryRunQueue(64, time.Second))
		}
	}()

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = runner.enqueueRun("task_x", "run_x")
			}
		}()
	}

	wg.Wait()
}

// TestRunnerGetQueueConsistency verifies that concurrent SetQueue writers and
// getQueue readers never observe a torn pointer and can safely call methods on
// the returned value.
func TestRunnerGetQueueConsistency(t *testing.T) {
	t.Parallel()
	runner := newTestRunner()
	runner.SetQueue(NewMemoryRunQueue(8, time.Second))

	var wg sync.WaitGroup
	const n = 20

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if q := runner.getQueue(); q != nil {
					_ = q.Capacity()
					_ = q.Backend()
				}
			}
		}()
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				runner.SetQueue(NewMemoryRunQueue(8, time.Second))
			}
		}()
	}

	wg.Wait()
}

// TestRunnerRuntimeStatsConcurrent exercises RuntimeStats from many goroutines
// simultaneously, ensuring the queue-pointer read and store reads don't race
// with concurrent SetQueue calls.
func TestRunnerRuntimeStatsConcurrent(t *testing.T) {
	t.Parallel()
	runner := newTestRunner()
	runner.SetQueue(NewMemoryRunQueue(8, time.Second))

	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 8

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = runner.RuntimeStats(ctx)
			}
		}()
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				runner.SetQueue(NewMemoryRunQueue(8, time.Second))
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
}
