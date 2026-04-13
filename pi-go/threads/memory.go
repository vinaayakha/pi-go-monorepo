package threads

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/vinaayakha/pi-go/ai"
)

// MemoryStore is an in-memory thread store. Not persistent across restarts.
type MemoryStore struct {
	mu      sync.RWMutex
	threads map[string]*Thread
	counter int
}

// NewMemoryStore creates a new in-memory thread store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{threads: make(map[string]*Thread)}
}

func (s *MemoryStore) Create(metadata map[string]string) (*Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	id := fmt.Sprintf("thread_%d_%d", time.Now().UnixNano(), s.counter)
	now := time.Now()
	t := &Thread{
		ID:        id,
		Messages:  nil,
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if t.Metadata == nil {
		t.Metadata = map[string]string{}
	}
	s.threads[id] = t
	return copyThread(t), nil
}

func (s *MemoryStore) Get(id string) (*Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.threads[id]
	if !ok {
		return nil, fmt.Errorf("thread not found: %s", id)
	}
	return copyThread(t), nil
}

func (s *MemoryStore) List() ([]*Thread, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Thread, 0, len(s.threads))
	for _, t := range s.threads {
		result = append(result, copyThread(t))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (s *MemoryStore) AppendMessages(id string, msgs []ai.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok {
		return fmt.Errorf("thread not found: %s", id)
	}
	t.Messages = append(t.Messages, msgs...)
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) SetMessages(id string, msgs []ai.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok {
		return fmt.Errorf("thread not found: %s", id)
	}
	t.Messages = append([]ai.Message{}, msgs...)
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) SetMetadata(id string, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok {
		return fmt.Errorf("thread not found: %s", id)
	}
	if t.Metadata == nil {
		t.Metadata = map[string]string{}
	}
	t.Metadata[key] = value
	t.UpdatedAt = time.Now()
	return nil
}

func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.threads[id]; !ok {
		return fmt.Errorf("thread not found: %s", id)
	}
	delete(s.threads, id)
	return nil
}

func copyThread(t *Thread) *Thread {
	msgs := make([]ai.Message, len(t.Messages))
	copy(msgs, t.Messages)
	meta := make(map[string]string, len(t.Metadata))
	for k, v := range t.Metadata {
		meta[k] = v
	}
	return &Thread{
		ID: t.ID, Messages: msgs, Metadata: meta,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}
