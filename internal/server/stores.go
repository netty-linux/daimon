package server

import (
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/threads"
	"sync"
)

// These adapters own in-process serialization. Share these same instances with
// HTTP and Sessions; never concurrently access their underlying stores elsewhere.
// A second process/writer remains unsupported by the domain stores.
type BotStore struct {
	mu    sync.Mutex
	store *bots.Store
}
type ThreadStore struct {
	mu    sync.Mutex
	store *threads.Store
}

func WrapBots(s *bots.Store) *BotStore {
	if s == nil {
		return nil
	}
	return &BotStore{store: s}
}
func WrapThreads(s *threads.Store) *ThreadStore {
	if s == nil {
		return nil
	}
	return &ThreadStore{store: s}
}
func (s *BotStore) List() ([]bots.Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.List()
}
func (s *BotStore) Get(id bots.ID) (bots.Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Get(id)
}
func (s *BotStore) Create(b bots.Bot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Create(b)
}
func (s *BotStore) Update(b bots.Bot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Update(b)
}
func (s *BotStore) Delete(id bots.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Delete(id)
}
func (s *ThreadStore) List() ([]threads.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.List()
}
func (s *ThreadStore) Get(id threads.ID) (threads.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Get(id)
}
func (s *ThreadStore) Create(t threads.Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Create(t)
}
func (s *ThreadStore) Update(t threads.Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Update(t)
}
func (s *ThreadStore) Delete(id threads.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Delete(id)
}
