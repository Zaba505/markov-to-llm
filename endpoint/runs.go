package endpoint

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"

	"github.com/Zaba505/markov-to-llm/graph"
	"github.com/Zaba505/markov-to-llm/markov"
)

// maxRuns is how many chains the server keeps. A chain can be large and the page
// that refers to it can be gone, with no way of telling, so that the least
// recently used chain makes way for a new one.
const maxRuns = 8

// run is an uploaded corpus and its chain. Its fields are read-only once made.
type run struct {
	id            string
	name          string
	preview       string
	previewIsPart bool
	chain         *markov.Chain
	graph         *graph.Builder
}

// runStore holds the most recently used runs.
type runStore struct {
	mu     sync.Mutex
	byID   map[string]*run
	recent []string // least recently used first
}

func newRunStore() *runStore {
	return &runStore{byID: make(map[string]*run, maxRuns+1)}
}

// add gives r a new ID and keeps it, evicting the least recently used run when
// the store is full.
func (s *runStore) add(r *run) {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	r.id = hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[r.id] = r
	s.recent = append(s.recent, r.id)
	if len(s.recent) > maxRuns {
		delete(s.byID, s.recent[0])
		s.recent = slices.Delete(s.recent, 0, 1)
	}
}

// get returns the run with the given ID, if the store still has it, and marks
// it as the most recently used.
func (s *runStore) get(id string) (*run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	i := slices.Index(s.recent, id)
	s.recent = append(slices.Delete(s.recent, i, i+1), id)
	return r, true
}
