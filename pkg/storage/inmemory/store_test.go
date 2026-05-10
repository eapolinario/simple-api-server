/*
Copyright 2026 Eduardo Apolinario.
*/

package inmemory

import (
	"strconv"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

func newStore() *Store {
	return New(v1alpha1.Resource("tasks"))
}

func newTask(ns, name, image string) *v1alpha1.Task {
	return &v1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       v1alpha1.TaskSpec{Image: image},
	}
}

func key(ns, name string) Key {
	return Key{Namespace: ns, Name: name}
}

func mustGetRV(t *testing.T, obj *v1alpha1.Task) uint64 {
	t.Helper()
	rv, err := strconv.ParseUint(obj.GetResourceVersion(), 10, 64)
	if err != nil {
		t.Fatalf("parsing resourceVersion %q: %v", obj.GetResourceVersion(), err)
	}
	return rv
}

func TestStore_Create_StampsRV(t *testing.T) {
	s := newStore()
	got, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "alpine"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rv := mustGetRV(t, got.(*v1alpha1.Task)); rv != 1 {
		t.Errorf("first Create RV = %d; want 1", rv)
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d; want 1", s.Len())
	}
}

func TestStore_Create_AlreadyExists(t *testing.T) {
	s := newStore()
	if _, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "a")); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "b"))
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("second Create err = %v; want IsAlreadyExists", err)
	}
}

func TestStore_Get_NotFound(t *testing.T) {
	_, err := newStore().Get(key("ns", "missing"))
	if !apierrors.IsNotFound(err) {
		t.Errorf("Get err = %v; want IsNotFound", err)
	}
}

func TestStore_Get_ReturnsDeepCopy(t *testing.T) {
	s := newStore()
	if _, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "alpine")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(key("ns", "t1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got.(*v1alpha1.Task).Spec.Image = "MUTATED"

	again, err := s.Get(key("ns", "t1"))
	if err != nil {
		t.Fatalf("re-Get: %v", err)
	}
	if again.(*v1alpha1.Task).Spec.Image != "alpine" {
		t.Errorf("Get returned a live reference; image leaked: %q", again.(*v1alpha1.Task).Spec.Image)
	}
}

func TestStore_Create_DeepCopiesInput(t *testing.T) {
	s := newStore()
	in := newTask("ns", "t1", "alpine")
	if _, err := s.Create(key("ns", "t1"), in); err != nil {
		t.Fatalf("Create: %v", err)
	}

	in.Spec.Image = "MUTATED"
	got, err := s.Get(key("ns", "t1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if img := got.(*v1alpha1.Task).Spec.Image; img != "alpine" {
		t.Errorf("post-Create caller mutation leaked into store: image = %q", img)
	}
}

func TestStore_List_FiltersByNamespace(t *testing.T) {
	s := newStore()
	for _, tt := range []struct{ ns, name string }{
		{"ns-a", "t1"}, {"ns-a", "t2"}, {"ns-b", "t3"},
	} {
		if _, err := s.Create(key(tt.ns, tt.name), newTask(tt.ns, tt.name, "x")); err != nil {
			t.Fatalf("Create %s/%s: %v", tt.ns, tt.name, err)
		}
	}

	for _, tc := range []struct {
		ns   string
		want int
	}{
		{"ns-a", 2}, {"ns-b", 1}, {"ns-missing", 0}, {"" /* all */, 3},
	} {
		got, err := s.List(tc.ns)
		if err != nil {
			t.Errorf("List(%q): %v", tc.ns, err)
			continue
		}
		if len(got) != tc.want {
			t.Errorf("List(%q) returned %d; want %d", tc.ns, len(got), tc.want)
		}
	}
}

func TestStore_Update_BumpsRV(t *testing.T) {
	s := newStore()
	created, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "alpine"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	createdRV := mustGetRV(t, created.(*v1alpha1.Task))

	updated, err := s.Update(key("ns", "t1"), newTask("ns", "t1", "alpine:edge"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	updatedRV := mustGetRV(t, updated.(*v1alpha1.Task))
	if updatedRV <= createdRV {
		t.Errorf("Update RV (%d) did not advance past Create RV (%d)", updatedRV, createdRV)
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	_, err := newStore().Update(key("ns", "missing"), newTask("ns", "missing", "x"))
	if !apierrors.IsNotFound(err) {
		t.Errorf("Update missing err = %v; want IsNotFound", err)
	}
}

func TestStore_Delete_RemovesAndReturnsCopy(t *testing.T) {
	s := newStore()
	if _, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "alpine")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Delete(key("ns", "t1"))
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got.(*v1alpha1.Task).Spec.Image != "alpine" {
		t.Errorf("Delete returned wrong object: %+v", got)
	}
	if s.Len() != 0 {
		t.Errorf("Len after delete = %d; want 0", s.Len())
	}
	if _, err := s.Get(key("ns", "t1")); !apierrors.IsNotFound(err) {
		t.Errorf("Get after Delete err = %v; want IsNotFound", err)
	}
}

func TestStore_Delete_NotFound(t *testing.T) {
	_, err := newStore().Delete(key("ns", "missing"))
	if !apierrors.IsNotFound(err) {
		t.Errorf("Delete missing err = %v; want IsNotFound", err)
	}
}

func TestStore_RV_BumpedOnDelete(t *testing.T) {
	s := newStore()
	if _, err := s.Create(key("ns", "t1"), newTask("ns", "t1", "alpine")); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rvBefore := s.CurrentResourceVersion()
	if _, err := s.Delete(key("ns", "t1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if rvAfter := s.CurrentResourceVersion(); rvAfter <= rvBefore {
		t.Errorf("Delete did not advance RV: before=%d after=%d", rvBefore, rvAfter)
	}
}

func TestStore_RV_MonotonicAcrossMixedOps(t *testing.T) {
	s := newStore()
	type op func() error
	steps := []op{
		func() error { _, e := s.Create(key("ns", "t1"), newTask("ns", "t1", "a")); return e },
		func() error { _, e := s.Create(key("ns", "t2"), newTask("ns", "t2", "b")); return e },
		func() error { _, e := s.Update(key("ns", "t1"), newTask("ns", "t1", "a-v2")); return e },
		func() error { _, e := s.Delete(key("ns", "t2")); return e },
		func() error { _, e := s.Update(key("ns", "t1"), newTask("ns", "t1", "a-v3")); return e },
	}

	prev := uint64(0)
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		rv := s.CurrentResourceVersion()
		if rv <= prev {
			t.Fatalf("step %d: RV %d did not advance past %d", i, rv, prev)
		}
		prev = rv
	}
}

func TestStore_Concurrent_NoRace(t *testing.T) {
	// Run with `go test -race` to actually catch races. This test exercises
	// every public method from many goroutines; the assertion is simply
	// "does not panic, every Create succeeds with a unique key".
	s := newStore()
	const writers = 16
	const itemsPerWriter = 25

	var wg sync.WaitGroup
	wg.Add(writers)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < itemsPerWriter; i++ {
				name := strconv.Itoa(w) + "-" + strconv.Itoa(i)
				if _, err := s.Create(key("ns", name), newTask("ns", name, "x")); err != nil {
					t.Errorf("writer %d: Create: %v", w, err)
				}
				if _, err := s.Get(key("ns", name)); err != nil {
					t.Errorf("writer %d: Get: %v", w, err)
				}
				if _, err := s.List("ns"); err != nil {
					t.Errorf("writer %d: List: %v", w, err)
				}
			}
		}(w)
	}
	wg.Wait()

	if s.Len() != writers*itemsPerWriter {
		t.Errorf("after %d writers x %d items, Len = %d; want %d",
			writers, itemsPerWriter, s.Len(), writers*itemsPerWriter)
	}
}

func TestKey_String(t *testing.T) {
	for _, tc := range []struct {
		k    Key
		want string
	}{
		{Key{Namespace: "ns", Name: "n"}, "ns/n"},
		{Key{Namespace: "", Name: "cluster-scoped"}, "cluster-scoped"},
	} {
		if got := tc.k.String(); got != tc.want {
			t.Errorf("Key{%s,%s}.String() = %q; want %q", tc.k.Namespace, tc.k.Name, got, tc.want)
		}
	}
}
