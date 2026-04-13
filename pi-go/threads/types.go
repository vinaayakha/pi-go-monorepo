// Package threads provides conversation persistence for the agent.
package threads

import (
	"time"

	"github.com/vinaayakha/pi-go/ai"
)

// Thread is a persisted conversation.
type Thread struct {
	ID        string            `json:"id"`
	Messages  []ai.Message      `json:"messages"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

// Store persists and retrieves conversation threads.
type Store interface {
	// Create a new thread with optional metadata.
	Create(metadata map[string]string) (*Thread, error)
	// Get a thread by ID.
	Get(id string) (*Thread, error)
	// List all threads, ordered by most recently updated.
	List() ([]*Thread, error)
	// AppendMessages adds messages to an existing thread.
	AppendMessages(id string, msgs []ai.Message) error
	// SetMessages replaces all messages in a thread.
	SetMessages(id string, msgs []ai.Message) error
	// SetMetadata sets a metadata key on a thread.
	SetMetadata(id string, key, value string) error
	// Delete removes a thread.
	Delete(id string) error
}
