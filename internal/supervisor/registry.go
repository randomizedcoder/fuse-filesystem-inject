package supervisor

import (
	"os"
	"sort"
	"sync"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/protocol"
)

// mountState is one live injected mount. req identifies it (and, via ID, keys
// the registry); proc is the GeeSFS process geesefsd owns — nil is allowed so
// tests can exercise the registry without a real process. Cleanup (Phase 5)
// reaps proc and unmounts using req.
type mountState struct {
	req  protocol.Request
	proc *os.Process
}

// registry tracks live mounts keyed by container id. It is safe for concurrent
// use: the daemon serves each connection in its own goroutine. Keying by id is
// what keeps containers isolated — one container's request can only ever touch
// its own entry.
type registry struct {
	mu sync.Mutex
	m  map[string]*mountState
}

func newRegistry() *registry {
	return &registry{m: make(map[string]*mountState)}
}

// add records st under its container id, replacing any prior entry.
func (r *registry) add(st *mountState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[st.req.ID] = st
}

// get returns the mount recorded for id, if any.
func (r *registry) get(id string) (*mountState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.m[id]
	return st, ok
}

// remove deletes and returns the mount recorded for id, if any.
func (r *registry) remove(id string) (*mountState, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.m[id]
	if ok {
		delete(r.m, id)
	}
	return st, ok
}

// ids returns the tracked container ids in sorted order (deterministic for
// logging and tests).
func (r *registry) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.m))
	for id := range r.m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
