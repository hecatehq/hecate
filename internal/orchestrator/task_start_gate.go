package orchestrator

import "sync"

type taskStartGate struct {
	mu    sync.Mutex
	locks map[string]*taskStartLock
}

type taskStartLock struct {
	mu   sync.Mutex
	refs int
}

func (gate *taskStartGate) lock(taskID string) func() {
	gate.mu.Lock()
	if gate.locks == nil {
		gate.locks = make(map[string]*taskStartLock)
	}
	entry := gate.locks[taskID]
	if entry == nil {
		entry = &taskStartLock{}
		gate.locks[taskID] = entry
	}
	entry.refs++
	gate.mu.Unlock()

	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			gate.mu.Lock()
			entry.refs--
			if entry.refs == 0 {
				delete(gate.locks, taskID)
			}
			gate.mu.Unlock()
		})
	}
}
