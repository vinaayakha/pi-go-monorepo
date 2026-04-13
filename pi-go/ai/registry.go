package ai

import (
	"context"
	"fmt"
	"sync"
)

// StreamFunc is the signature for provider stream functions.
type StreamFunc func(model Model, ctx Context, opts *StreamOptions) *EventStream

// SimpleStreamFunc is the signature for provider streamSimple functions.
type SimpleStreamFunc func(model Model, ctx Context, opts *SimpleStreamOptions) *EventStream

// APIProvider bundles stream functions for a specific API.
type APIProvider struct {
	API          API
	Stream       StreamFunc
	StreamSimple SimpleStreamFunc
}

var (
	registryMu sync.RWMutex
	registry   = map[API]*APIProvider{}
)

// RegisterAPIProvider adds or replaces a provider in the global registry.
func RegisterAPIProvider(p *APIProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.API] = p
}

// GetAPIProvider looks up a provider by API identifier.
func GetAPIProvider(api API) (*APIProvider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[api]
	return p, ok
}

// UnregisterAPIProvider removes a provider.
func UnregisterAPIProvider(api API) {
	registryMu.Lock()
	defer registryMu.Unlock()
	delete(registry, api)
}

// ClearAPIProviders removes all providers.
func ClearAPIProviders() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = map[API]*APIProvider{}
}

// ── Top-level stream helpers (like stream.ts) ───────────────────────

// Stream calls the raw provider stream function for the given model.
func Stream(model Model, ctx Context, opts *StreamOptions) (*EventStream, error) {
	p, ok := GetAPIProvider(model.API)
	if !ok {
		return nil, fmt.Errorf("no API provider registered for api: %s", model.API)
	}
	return p.Stream(model, ctx, opts), nil
}

// StreamSimple calls the simplified provider stream function.
func StreamSimple(model Model, ctx Context, opts *SimpleStreamOptions) (*EventStream, error) {
	p, ok := GetAPIProvider(model.API)
	if !ok {
		return nil, fmt.Errorf("no API provider registered for api: %s", model.API)
	}
	return p.StreamSimple(model, ctx, opts), nil
}

// Complete streams to completion and returns the final message.
func Complete(goCtx context.Context, model Model, aiCtx Context, opts *StreamOptions) (*AssistantMessage, error) {
	es, err := Stream(model, aiCtx, opts)
	if err != nil {
		return nil, err
	}
	// Drain events
	for range es.Events() {
	}
	return es.Result(), nil
}
