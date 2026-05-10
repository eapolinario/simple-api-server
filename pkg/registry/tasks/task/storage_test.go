/*
Copyright 2026 Eduardo Apolinario.
*/

package task

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
	"github.com/eapolinario/simple-api-server/pkg/storage/inmemory"
)

var taskGR = schema.GroupResource{Group: v1alpha1.GroupName, Resource: "tasks"}

// fixture returns a fresh REST + StatusREST pair sharing one store.
// Tests must not share fixtures because the in-memory store is mutated.
func fixture(t *testing.T) (*REST, *StatusREST, *inmemory.Store) {
	t.Helper()
	scheme := newScheme(t)
	store := inmemory.New(taskGR)
	r := NewREST(store, NewStrategy(scheme), taskGR)
	sr := NewStatusREST(store, NewStatusStrategy(scheme), taskGR)
	return r, sr, store
}

// nsCtx returns a context whose namespace is ns. Required for any REST
// method that reads namespace from the request.
func nsCtx(ns string) context.Context {
	return genericapirequest.WithNamespace(genericapirequest.NewContext(), ns)
}

func TestREST_Create_Success(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	in := validTask("t1")
	out, err := r.Create(ctx, in, nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := out.(*v1alpha1.Task)
	if got.Name != "t1" {
		t.Errorf("Name = %q, want t1", got.Name)
	}
	if got.Generation != 1 {
		t.Errorf("Generation = %d, want 1", got.Generation)
	}
	if got.ResourceVersion == "" {
		t.Error("ResourceVersion should be stamped")
	}
}

func TestREST_Create_AlreadyExists(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	if _, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		t.Errorf("err = %v, want AlreadyExists", err)
	}
}

func TestREST_Create_StrategyValidationRejectsEmptyImage(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	in := validTask("t1")
	in.Spec.Image = ""
	_, err := r.Create(ctx, in, nil, &metav1.CreateOptions{})
	if !apierrors.IsInvalid(err) {
		t.Errorf("err = %v, want Invalid", err)
	}
}

func TestREST_Create_DropsClientStatus(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	in := validTask("t1")
	in.Status.Phase = "Running"
	out, err := r.Create(ctx, in, nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.(*v1alpha1.Task).Status.Phase != "" {
		t.Errorf("client-supplied status was not dropped on create")
	}
}

func TestREST_Create_CreateValidationHook(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	called := false
	cv := func(_ context.Context, _ runtime.Object) error {
		called = true
		return apierrors.NewForbidden(taskGR, "t1", nil)
	}
	_, err := r.Create(ctx, validTask("t1"), cv, &metav1.CreateOptions{})
	if !called {
		t.Error("createValidation was not invoked")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("err = %v, want Forbidden from createValidation", err)
	}
}

func TestREST_Get_NotFound(t *testing.T) {
	r, _, _ := fixture(t)
	_, err := r.Get(nsCtx("ns"), "missing", &metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestREST_Get_NamespaceRequired(t *testing.T) {
	r, _, _ := fixture(t)
	_, err := r.Get(genericapirequest.NewContext(), "t1", &metav1.GetOptions{})
	if !apierrors.IsBadRequest(err) {
		t.Errorf("err = %v, want BadRequest when namespace missing", err)
	}
}

func TestREST_List_FiltersByNamespaceAndLabel(t *testing.T) {
	r, _, _ := fixture(t)

	mk := func(ns, name string, lbls map[string]string) *v1alpha1.Task {
		tk := validTask(name)
		tk.Namespace = ns
		tk.Labels = lbls
		return tk
	}
	for _, tk := range []*v1alpha1.Task{
		mk("ns", "a", map[string]string{"team": "blue"}),
		mk("ns", "b", map[string]string{"team": "red"}),
		mk("other", "c", map[string]string{"team": "blue"}),
	} {
		if _, err := r.Create(nsCtx(tk.Namespace), tk, nil, &metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed Create %s/%s: %v", tk.Namespace, tk.Name, err)
		}
	}

	sel, err := labels.Parse("team=blue")
	if err != nil {
		t.Fatalf("labels.Parse: %v", err)
	}
	out, err := r.List(nsCtx("ns"), &metainternalversion.ListOptions{LabelSelector: sel})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	list := out.(*v1alpha1.TaskList)
	if len(list.Items) != 1 || list.Items[0].Name != "a" {
		t.Errorf("List items = %+v, want [a]", taskNames(list))
	}
	if list.ResourceVersion == "" {
		t.Error("List ResourceVersion must be stamped")
	}
}

func TestREST_List_RejectsFieldSelector(t *testing.T) {
	r, _, _ := fixture(t)
	sel := fields.OneTermEqualSelector("metadata.name", "x")
	_, err := r.List(nsCtx("ns"), &metainternalversion.ListOptions{FieldSelector: sel})
	if err == nil {
		t.Fatal("expected error for field selector, got nil")
	}
	if !strings.Contains(err.Error(), "field selectors are not supported") {
		t.Errorf("err = %v, want field-selector rejection", err)
	}
}

func TestREST_Update_BumpsGenerationOnSpecChange(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	created, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.Spec.Image = "alpine:4"

	updated, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := updated.(*v1alpha1.Task)
	if got.Generation != 2 {
		t.Errorf("Generation = %d, want 2 after spec change", got.Generation)
	}
}

func TestREST_Update_PreservesGenerationWhenSpecUnchanged(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	created, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.Labels = map[string]string{"k": "v"}

	updated, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := updated.(*v1alpha1.Task).Generation; got != 1 {
		t.Errorf("Generation = %d, want 1 with unchanged spec", got)
	}
}

func TestREST_Update_RVConflict(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	created, _ := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.ResourceVersion = "999"
	tk.Spec.Image = "alpine:4"

	_, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if !apierrors.IsConflict(err) {
		t.Errorf("err = %v, want Conflict", err)
	}
}

func TestREST_Update_RVEmptyRejected(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	created, _ := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.ResourceVersion = "" // strategy.AllowUnconditionalUpdate is false

	_, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if !apierrors.IsConflict(err) {
		t.Errorf("err = %v, want Conflict when RV is empty", err)
	}
}

func TestREST_Update_NotFoundDoesNotCreate(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	tk := validTask("missing")
	_, _, err := r.Update(ctx, "missing", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound (AllowCreateOnUpdate=false)", err)
	}
}

func TestREST_Update_StatusIgnoredOnSpecPath(t *testing.T) {
	// Spec strategy must preserve old status even if the client tries to
	// mutate it via the main resource endpoint.
	r, _, store := fixture(t)
	ctx := nsCtx("ns")

	created, _ := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	// Seed a status via the store directly so we can verify the spec path
	// does not overwrite it.
	existing, _ := store.Get(inmemory.Key{Namespace: "ns", Name: "t1"})
	existing.(*v1alpha1.Task).Status.Phase = "Running"
	if _, err := store.Update(inmemory.Key{Namespace: "ns", Name: "t1"}, existing); err != nil {
		t.Fatalf("seed status: %v", err)
	}
	// Refresh RV for the client-side write.
	refreshed, _ := r.Get(ctx, "t1", &metav1.GetOptions{})
	tk := refreshed.(*v1alpha1.Task).DeepCopy()
	tk.Spec.Image = "alpine:4"
	tk.Status.Phase = "Failed" // client attempt to mutate status via spec PUT

	_ = created
	updated, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := updated.(*v1alpha1.Task).Status.Phase; got != "Running" {
		t.Errorf("Status.Phase = %q, want Running (spec PUT must not mutate status)", got)
	}
}

func TestREST_Delete_Success(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	if _, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	out, immediate, err := r.Delete(ctx, "t1", nil, &metav1.DeleteOptions{})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !immediate {
		t.Error("immediate = false, want true (no graceful deletion)")
	}
	if out.(*v1alpha1.Task).Name != "t1" {
		t.Errorf("Delete returned %+v, want task t1", out)
	}
	if _, err := r.Get(ctx, "t1", &metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("post-Delete Get err = %v, want NotFound", err)
	}
}

func TestREST_Delete_PreconditionRVMismatch(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")

	if _, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stale := "999"
	_, _, err := r.Delete(ctx, "t1", nil, &metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{ResourceVersion: &stale},
	})
	if !apierrors.IsConflict(err) {
		t.Errorf("err = %v, want Conflict on RV precondition mismatch", err)
	}
}

func TestREST_Delete_ValidationHookCalled(t *testing.T) {
	r, _, _ := fixture(t)
	ctx := nsCtx("ns")
	if _, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	called := false
	dv := func(_ context.Context, _ runtime.Object) error {
		called = true
		return apierrors.NewForbidden(taskGR, "t1", nil)
	}
	_, _, err := r.Delete(ctx, "t1", dv, &metav1.DeleteOptions{})
	if !called {
		t.Error("deleteValidation was not invoked")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("err = %v, want Forbidden from deleteValidation", err)
	}
}

func TestREST_Watch_Returns405(t *testing.T) {
	r, _, _ := fixture(t)
	_, err := r.Watch(nsCtx("ns"), &metainternalversion.ListOptions{})
	if err == nil {
		t.Fatal("Watch returned nil error; must be MethodNotSupported until watch lands")
	}
	if !apierrors.IsMethodNotSupported(err) {
		t.Errorf("err = %v, want MethodNotSupported (HTTP 405)", err)
	}
}

func TestREST_Interfaces_NamespacedAndNamed(t *testing.T) {
	r, _, _ := fixture(t)
	if !r.NamespaceScoped() {
		t.Error("REST.NamespaceScoped must be true")
	}
	if r.GetSingularName() != "task" {
		t.Errorf("singular = %q, want task", r.GetSingularName())
	}
	if !contains(r.ShortNames(), "tk") {
		t.Errorf("short names = %v, want to contain tk", r.ShortNames())
	}
}

func TestStatusREST_Update_PreservesSpecAndGeneration(t *testing.T) {
	r, sr, _ := fixture(t)
	ctx := nsCtx("ns")

	created, err := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	tk := created.(*v1alpha1.Task).DeepCopy()
	// Client attempts to mutate spec + generation via /status — should be ignored.
	tk.Spec.Image = "alpine:malicious"
	tk.Generation = 999
	tk.Status.Phase = "Running"
	tk.Status.ObservedGeneration = 1

	updated, _, err := sr.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("StatusREST.Update: %v", err)
	}
	got := updated.(*v1alpha1.Task)
	if got.Spec.Image != "alpine:3" {
		t.Errorf("Spec.Image = %q, want alpine:3 (status path must not mutate spec)", got.Spec.Image)
	}
	if got.Generation != 1 {
		t.Errorf("Generation = %d, want 1 (status path must not bump generation)", got.Generation)
	}
	if got.Status.Phase != "Running" {
		t.Errorf("Status.Phase = %q, want Running", got.Status.Phase)
	}
}

func TestStatusREST_Update_ObservedGenerationValidation(t *testing.T) {
	r, sr, _ := fixture(t)
	ctx := nsCtx("ns")

	created, _ := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.Status.ObservedGeneration = 99 // generation is 1; should reject

	_, _, err := sr.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if !apierrors.IsInvalid(err) {
		t.Errorf("err = %v, want Invalid (observedGeneration > generation)", err)
	}
}

func TestStatusREST_Update_NotFound(t *testing.T) {
	_, sr, _ := fixture(t)
	tk := validTask("missing")
	_, _, err := sr.Update(nsCtx("ns"), "missing", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("err = %v, want NotFound", err)
	}
}

func TestREST_RVMonotonicAcrossSpecAndStatus(t *testing.T) {
	r, sr, _ := fixture(t)
	ctx := nsCtx("ns")

	created, _ := r.Create(ctx, validTask("t1"), nil, &metav1.CreateOptions{})
	rv0 := parseRVOrZero(created.(*v1alpha1.Task).ResourceVersion)

	tk := created.(*v1alpha1.Task).DeepCopy()
	tk.Spec.Image = "alpine:4"
	upd, _, err := r.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update spec: %v", err)
	}
	rv1 := parseRVOrZero(upd.(*v1alpha1.Task).ResourceVersion)
	if rv1 <= rv0 {
		t.Errorf("RV did not advance after spec update: %d -> %d", rv0, rv1)
	}

	tk2 := upd.(*v1alpha1.Task).DeepCopy()
	tk2.Status.Phase = "Running"
	tk2.Status.ObservedGeneration = tk2.Generation
	upd2, _, err := sr.Update(ctx, "t1", rest.DefaultUpdatedObjectInfo(tk2), nil, nil, false, &metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update status: %v", err)
	}
	rv2 := parseRVOrZero(upd2.(*v1alpha1.Task).ResourceVersion)
	if rv2 <= rv1 {
		t.Errorf("RV did not advance after status update: %d -> %d", rv1, rv2)
	}
}

// --- test helpers ---

func taskNames(list *v1alpha1.TaskList) []string {
	out := make([]string, 0, len(list.Items))
	for _, t := range list.Items {
		out = append(out, t.Name)
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
