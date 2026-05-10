/*
Copyright 2026 Eduardo Apolinario.
*/

package integration

import (
	"context"
	"strconv"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

// tasksGVR is the only GroupVersionResource served by this apiserver
// today. Centralizing it here keeps the tests honest if the version
// ever moves.
var tasksGVR = schema.GroupVersionResource{
	Group:    v1alpha1.GroupName,
	Version:  v1alpha1.SchemeGroupVersion.Version,
	Resource: "tasks",
}

// newTask returns a minimal valid Task as an Unstructured suitable for
// the dynamic client. Image is required (strategy enforces it); other
// fields are exercised by individual tests.
func newTask(name, image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": v1alpha1.SchemeGroupVersion.String(),
			"kind":       "Task",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "default",
			},
			"spec": map[string]any{
				"image": image,
			},
		},
	}
}

// testClient boots a server and returns a namespace-scoped dynamic
// client for tasks/default.
func testClient(t *testing.T) (context.Context, dynamic.ResourceInterface) {
	t.Helper()
	cfg := StartTestServer(t)
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build dynamic client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx, dc.Resource(tasksGVR).Namespace("default")
}

// TestRoundTrip_CreateGetListDelete is the smallest non-trivial proof
// that the apiserver is actually serving our API: create → get → list →
// delete → get(NotFound). If this fails the rest of the suite is
// meaningless, so it leads.
func TestRoundTrip_CreateGetListDelete(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("hello", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := created.GetName(); got != "hello" {
		t.Errorf("create returned name=%q; want %q", got, "hello")
	}
	if image, _, _ := unstructured.NestedString(created.Object, "spec", "image"); image != "alpine:3.20" {
		t.Errorf("create returned spec.image=%q; want %q", image, "alpine:3.20")
	}
	if gen := created.GetGeneration(); gen != 1 {
		t.Errorf("create returned generation=%d; want 1 (strategy seeds it)", gen)
	}
	if uid := created.GetUID(); uid == "" {
		t.Error("create returned empty UID; server should assign one")
	}
	if rv := created.GetResourceVersion(); rv == "" {
		t.Error("create returned empty resourceVersion")
	}

	got, err := tc.Get(ctx, "hello", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.GetUID() != created.GetUID() {
		t.Errorf("get UID=%q; want create UID=%q", got.GetUID(), created.GetUID())
	}

	list, err := tc.List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list returned %d items; want 1", len(list.Items))
	}
	if list.Items[0].GetName() != "hello" {
		t.Errorf("list item name=%q; want %q", list.Items[0].GetName(), "hello")
	}

	if err := tc.Delete(ctx, "hello", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := tc.Get(ctx, "hello", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get after delete: err=%v; want NotFound", err)
	}
}

// TestCreate_RejectsEmptyImage proves the validation strategy is wired
// end-to-end. The unit-level strategy tests cover the same condition,
// but seeing the typed Invalid error survive the HTTP round-trip is
// the only way to know the apiserver actually invokes Validate before
// persisting.
func TestCreate_RejectsEmptyImage(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	bad := newTask("no-image", "")
	unstructured.RemoveNestedField(bad.Object, "spec", "image")

	_, err := tc.Create(ctx, bad, metav1.CreateOptions{})
	if err == nil {
		t.Fatal("create with empty spec.image succeeded; want Invalid")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("create error type: %v; want Invalid", err)
	}
}

// TestSpecUpdate_BumpsGeneration_PreservesStatus exercises the
// PrepareForUpdate contract: a non-status update must preserve the
// previous Status block (status is owned by the /status subresource),
// and metadata.generation increments iff spec actually changed.
func TestSpecUpdate_BumpsGeneration_PreservesStatus(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("upd", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Seed status via the subresource so we have something concrete to
	// assert is preserved by the spec update.
	stamped := created.DeepCopy()
	_ = unstructured.SetNestedField(stamped.Object, "Pending", "status", "phase")
	_ = unstructured.SetNestedField(stamped.Object, int64(1), "status", "observedGeneration")
	statusUpdated, err := tc.UpdateStatus(ctx, stamped, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if phase, _, _ := unstructured.NestedString(statusUpdated.Object, "status", "phase"); phase != "Pending" {
		t.Fatalf("status update returned phase=%q; want Pending", phase)
	}

	// Now mutate spec via the main resource endpoint. Status must
	// survive; generation must bump from 1 to 2.
	specUpdate := statusUpdated.DeepCopy()
	_ = unstructured.SetNestedField(specUpdate.Object, "alpine:3.21", "spec", "image")
	specUpdated, err := tc.Update(ctx, specUpdate, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("spec update: %v", err)
	}
	if gen := specUpdated.GetGeneration(); gen != 2 {
		t.Errorf("spec update generation=%d; want 2", gen)
	}
	if phase, _, _ := unstructured.NestedString(specUpdated.Object, "status", "phase"); phase != "Pending" {
		t.Errorf("spec update lost status.phase=%q; want Pending", phase)
	}
	if og, _, _ := unstructured.NestedInt64(specUpdated.Object, "status", "observedGeneration"); og != 1 {
		t.Errorf("spec update lost status.observedGeneration=%d; want 1", og)
	}

	// No-op spec update must NOT bump generation.
	noop := specUpdated.DeepCopy()
	noopUpdated, err := tc.Update(ctx, noop, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("no-op spec update: %v", err)
	}
	if gen := noopUpdated.GetGeneration(); gen != 2 {
		t.Errorf("no-op spec update generation=%d; want 2 (unchanged)", gen)
	}
}

// TestStatusUpdate_DropsSpecMutation is the symmetric check: when a
// client sends a status update that also tries to mutate spec, the
// spec mutation must be silently ignored (StatusStrategy pins spec to
// its previous value).
func TestStatusUpdate_DropsSpecMutation(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("status-only", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sneaky := created.DeepCopy()
	_ = unstructured.SetNestedField(sneaky.Object, "evil:latest", "spec", "image")
	_ = unstructured.SetNestedField(sneaky.Object, "Running", "status", "phase")

	got, err := tc.UpdateStatus(ctx, sneaky, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if image, _, _ := unstructured.NestedString(got.Object, "spec", "image"); image != "alpine:3.20" {
		t.Errorf("status update mutated spec.image=%q; want alpine:3.20 (StatusStrategy must pin spec)", image)
	}
	if phase, _, _ := unstructured.NestedString(got.Object, "status", "phase"); phase != "Running" {
		t.Errorf("status update phase=%q; want Running", phase)
	}
}

// TestStatusUpdate_RejectsObservedGenerationOverflow proves the
// observedGeneration discipline encoded in validateStatusUpdate
// (status.observedGeneration must not exceed metadata.generation) is
// enforced over the wire.
func TestStatusUpdate_RejectsObservedGenerationOverflow(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("og-ahead", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if gen := created.GetGeneration(); gen != 1 {
		t.Fatalf("created generation=%d; want 1", gen)
	}

	bad := created.DeepCopy()
	_ = unstructured.SetNestedField(bad.Object, int64(99), "status", "observedGeneration")
	_, err = tc.UpdateStatus(ctx, bad, metav1.UpdateOptions{})
	if err == nil {
		t.Fatal("status update with observedGeneration > generation succeeded; want Invalid")
	}
	if !apierrors.IsInvalid(err) {
		t.Errorf("status update error type: %v; want Invalid", err)
	}
}

// TestResourceVersion_MonotonicAcrossSpecAndStatus exercises the
// monotonic-RV contract: every successful write (spec OR status) must
// produce a strictly greater resourceVersion than the previous one. A
// regression here would silently break list-then-watch semantics later
// when watch is implemented.
func TestResourceVersion_MonotonicAcrossSpecAndStatus(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("rv", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rvs := []string{created.GetResourceVersion()}

	// Alternate spec and status writes a few times. Each must bump RV.
	current := created
	for i := 0; i < 3; i++ {
		// status write
		stat := current.DeepCopy()
		_ = unstructured.SetNestedField(stat.Object, "Pending", "status", "phase")
		_ = unstructured.SetNestedField(stat.Object, int64(i), "status", "observedGeneration")
		stat, err = tc.UpdateStatus(ctx, stat, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("status update %d: %v", i, err)
		}
		rvs = append(rvs, stat.GetResourceVersion())

		// spec write (genuinely different to force a generation bump
		// and a guaranteed PrepareForUpdate side effect)
		spec := stat.DeepCopy()
		_ = unstructured.SetNestedField(spec.Object, []any{"echo", strconv.Itoa(i)}, "spec", "command")
		spec, err = tc.Update(ctx, spec, metav1.UpdateOptions{})
		if err != nil {
			t.Fatalf("spec update %d: %v", i, err)
		}
		rvs = append(rvs, spec.GetResourceVersion())
		current = spec
	}

	for i := 1; i < len(rvs); i++ {
		prev, _ := strconv.ParseUint(rvs[i-1], 10, 64)
		cur, _ := strconv.ParseUint(rvs[i], 10, 64)
		if cur <= prev {
			t.Errorf("rv[%d]=%s not greater than rv[%d]=%s", i, rvs[i], i-1, rvs[i-1])
		}
	}
}

// TestUpdate_StaleResourceVersionConflicts proves optimistic
// concurrency works end-to-end: an update carrying a stale RV must
// fail with Conflict, not silently overwrite.
func TestUpdate_StaleResourceVersionConflicts(t *testing.T) {
	t.Parallel()
	ctx, tc := testClient(t)

	created, err := tc.Create(ctx, newTask("conflict", "alpine:3.20"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// First update succeeds and advances RV on the server.
	first := created.DeepCopy()
	_ = unstructured.SetNestedField(first.Object, "alpine:3.21", "spec", "image")
	if _, err := tc.Update(ctx, first, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("first update: %v", err)
	}

	// Second update from a client that still holds the original RV
	// must Conflict — its rv string is stale.
	stale := created.DeepCopy()
	_ = unstructured.SetNestedField(stale.Object, "alpine:3.22", "spec", "image")
	_, err = tc.Update(ctx, stale, metav1.UpdateOptions{})
	if err == nil {
		t.Fatal("stale-RV update succeeded; want Conflict")
	}
	if !apierrors.IsConflict(err) {
		t.Errorf("stale-RV update error type: %v; want Conflict", err)
	}
}
