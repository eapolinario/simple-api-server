/*
Copyright 2026 Eduardo Apolinario.
*/

// Package inmemory provides the in-memory storage backend used by every
// REST registry in this apiserver. The store is a map guarded by a
// sync.RWMutex with a monotonic uint64 resourceVersion. There is no
// persistence — server restart wipes state, by design.
//
// Implemented in the inmemory-store todo.
package inmemory
