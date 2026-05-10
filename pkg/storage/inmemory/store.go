/*
Copyright 2026 Eduardo Apolinario.
*/

// Package inmemory provides the in-memory storage backend used by every
// REST registry in this apiserver. The store is a map guarded by a
// sync.RWMutex with a monotonic uint64 resourceVersion. There is no
// persistence — server restart wipes state, by design.
//
// The store is intentionally minimal: it does not implement
// k8s.io/apiserver/pkg/storage.Interface. The registry layer wraps a
// *Store and translates between REST semantics and these primitives.
//
// Watch is deferred. The store has no notion of subscribers, replay
// buffers, or bookmarks. Any future watch implementation must deal with
// resourceVersion ordering across mixed write operations including
// deletes (this store bumps resourceVersion on Delete for that reason —
// see TestStore_RV_BumpedOnDelete).
package inmemory

import (
	"fmt"
	"strconv"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Key identifies an object within a Store. Cluster-scoped objects use an
// empty Namespace.
type Key struct {
	Namespace string
	Name      string
}

// String renders the key in `namespace/name` form (or just `name` when
// cluster-scoped). Used for error messages and logging.
func (k Key) String() string {
	if k.Namespace == "" {
		return k.Name
	}
	return k.Namespace + "/" + k.Name
}

// Store is an in-memory object store with monotonic resourceVersion.
//
// Concurrency: all methods are goroutine-safe. Reads use an RLock; writes
// use a Lock.
//
// Aliasing: callers may freely mutate objects passed to Create/Update —
// the store deep-copies on write. Likewise, mutating values returned by
// Get/List/Delete does not affect stored state — the store deep-copies on
// read. This is intentional and tested.
type Store struct {
	mu    sync.RWMutex
	items map[Key]runtime.Object
	rv    uint64
	gr    schema.GroupResource
}

// New returns an empty Store. gr is used to construct typed errors
// (NewNotFound / NewAlreadyExists) so callers receive errors with correct
// API metadata.
func New(gr schema.GroupResource) *Store {
	return &Store{
		items: map[Key]runtime.Object{},
		gr:    gr,
	}
}

// Create stores obj at key. Returns NewAlreadyExists if the key is
// occupied. On success returns a deep copy of the stored object with its
// resourceVersion stamped.
func (s *Store) Create(key Key, obj runtime.Object) (runtime.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[key]; ok {
		return nil, apierrors.NewAlreadyExists(s.gr, key.Name)
	}
	stored := obj.DeepCopyObject()
	if err := s.bumpAndStampLocked(stored); err != nil {
		return nil, err
	}
	s.items[key] = stored
	return stored.DeepCopyObject(), nil
}

// Get returns a deep copy of the object stored at key, or NewNotFound.
func (s *Store) Get(key Key) (runtime.Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	obj, ok := s.items[key]
	if !ok {
		return nil, apierrors.NewNotFound(s.gr, key.Name)
	}
	return obj.DeepCopyObject(), nil
}

// List returns deep copies of all stored objects whose key matches the
// namespace selector. An empty namespace returns objects across all
// namespaces (cluster-wide list semantics). The return order is
// unspecified; callers needing stable order must sort.
func (s *Store) List(namespace string) ([]runtime.Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]runtime.Object, 0, len(s.items))
	for k, v := range s.items {
		if namespace != "" && k.Namespace != namespace {
			continue
		}
		out = append(out, v.DeepCopyObject())
	}
	return out, nil
}

// Update replaces the object at key. Returns NewNotFound if the key does
// not exist. On success returns a deep copy of the stored object with its
// resourceVersion bumped.
//
// Update does not validate that the caller's resourceVersion matches the
// current one — that conflict-detection lives in the registry/strategy
// layer, not here.
func (s *Store) Update(key Key, obj runtime.Object) (runtime.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[key]; !ok {
		return nil, apierrors.NewNotFound(s.gr, key.Name)
	}
	stored := obj.DeepCopyObject()
	if err := s.bumpAndStampLocked(stored); err != nil {
		return nil, err
	}
	s.items[key] = stored
	return stored.DeepCopyObject(), nil
}

// Delete removes the object at key and bumps the store's resourceVersion
// so that future watch implementations can order deletion events
// correctly. Returns the deleted object (deep copy) or NewNotFound.
func (s *Store) Delete(key Key) (runtime.Object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.items[key]
	if !ok {
		return nil, apierrors.NewNotFound(s.gr, key.Name)
	}
	delete(s.items, key)
	s.rv++ // bump even on delete for future watch ordering
	return obj.DeepCopyObject(), nil
}

// CurrentResourceVersion returns the most recent resourceVersion stamped
// or consumed by the store. Exposed for tests and diagnostics; not part
// of any REST surface.
func (s *Store) CurrentResourceVersion() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rv
}

// Len returns the number of objects currently stored. Cheap; useful for
// assertions in tests.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.items)
}

// bumpAndStampLocked must be called with s.mu held for writing.
func (s *Store) bumpAndStampLocked(obj runtime.Object) error {
	s.rv++
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return fmt.Errorf("inmemory: cannot stamp resourceVersion: %w", err)
	}
	accessor.SetResourceVersion(strconv.FormatUint(s.rv, 10))
	return nil
}
