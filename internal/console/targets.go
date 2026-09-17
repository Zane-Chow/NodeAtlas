package console

import (
	"net/url"
	"sync"
)

type MemoryTargetStore struct {
	mutex   sync.Mutex
	targets map[string]*url.URL
}

func NewMemoryTargetStore() *MemoryTargetStore {
	return &MemoryTargetStore{targets: make(map[string]*url.URL)}
}

func (store *MemoryTargetStore) Put(sessionID string, target *url.URL) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	copy := *target
	store.targets[sessionID] = &copy
}

func (store *MemoryTargetStore) Take(sessionID string) (*url.URL, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	target, ok := store.targets[sessionID]
	delete(store.targets, sessionID)
	return target, ok
}

func (store *MemoryTargetStore) Delete(sessionID string) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.targets, sessionID)
}
