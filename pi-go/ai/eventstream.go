package ai

import "sync"

// EventStream is a concurrent push/pull async event channel.
// Push events from one goroutine, iterate from another.
type EventStream struct {
	ch     chan AssistantMessageEvent
	once   sync.Once
	result chan *AssistantMessage
	final  *AssistantMessage
	mu     sync.Mutex
}

// NewEventStream creates a buffered event stream.
func NewEventStream(bufSize int) *EventStream {
	if bufSize < 16 {
		bufSize = 16
	}
	return &EventStream{
		ch:     make(chan AssistantMessageEvent, bufSize),
		result: make(chan *AssistantMessage, 1),
	}
}

// Push sends an event into the stream.
func (s *EventStream) Push(event AssistantMessageEvent) {
	if event.Type == EventDone && event.Message != nil {
		s.mu.Lock()
		s.final = event.Message
		s.mu.Unlock()
	} else if event.Type == EventError && event.Error != nil {
		s.mu.Lock()
		s.final = event.Error
		s.mu.Unlock()
	}
	s.ch <- event
}

// End closes the stream. Must be called exactly once by the producer.
func (s *EventStream) End() {
	s.once.Do(func() {
		close(s.ch)
		s.mu.Lock()
		f := s.final
		s.mu.Unlock()
		if f != nil {
			s.result <- f
		}
		close(s.result)
	})
}

// Events returns a receive-only channel for consuming events.
func (s *EventStream) Events() <-chan AssistantMessageEvent {
	return s.ch
}

// Result blocks until the stream completes and returns the final AssistantMessage.
func (s *EventStream) Result() *AssistantMessage {
	msg, ok := <-s.result
	if !ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.final
	}
	return msg
}
