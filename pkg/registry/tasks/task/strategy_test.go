/*
Copyright 2026 Eduardo Apolinario.
*/

package task

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func validTask(name string) *v1alpha1.Task {
	return &v1alpha1.Task{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
		},
		Spec: v1alpha1.TaskSpec{
			Image: "alpine:3",
		},
	}
}

func TestStrategy_NamespaceScoped(t *testing.T) {
	if !NewStrategy(newScheme(t)).NamespaceScoped() {
		t.Error("Task strategy must be namespace scoped")
	}
}

func TestStrategy_DoesNotAllowCreateOnUpdate(t *testing.T) {
	if NewStrategy(newScheme(t)).AllowCreateOnUpdate() {
		t.Error("AllowCreateOnUpdate must be false; PUT to missing should 404")
	}
}

func TestStrategy_DoesNotAllowUnconditionalUpdate(t *testing.T) {
	if NewStrategy(newScheme(t)).AllowUnconditionalUpdate() {
		t.Error("AllowUnconditionalUpdate must be false; require resourceVersion")
	}
}

func TestStrategy_PrepareForCreate_DropsClientStatusAndStartsGenerationAtOne(t *testing.T) {
	s := NewStrategy(newScheme(t))
	in := validTask("t1")
	in.Status = v1alpha1.TaskStatus{
		Phase:              v1alpha1.TaskPhaseSucceeded,
		ObservedGeneration: 99,
		Conditions: []metav1.Condition{{
			Type:   v1alpha1.TaskConditionReady,
			Status: metav1.ConditionTrue,
		}},
	}
	in.Generation = 47

	s.PrepareForCreate(context.Background(), in)

	if in.Status.Phase != "" || in.Status.ObservedGeneration != 0 || len(in.Status.Conditions) != 0 {
		t.Errorf("status not cleared on create: %+v", in.Status)
	}
	if in.Generation != 1 {
		t.Errorf("generation = %d; want 1", in.Generation)
	}
}

func TestStrategy_PrepareForUpdate_PreservesStatusFromOld(t *testing.T) {
	s := NewStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 5
	old.Status = v1alpha1.TaskStatus{
		Phase:              v1alpha1.TaskPhaseRunning,
		ObservedGeneration: 5,
	}
	new := old.DeepCopy()
	new.Status = v1alpha1.TaskStatus{Phase: v1alpha1.TaskPhaseFailed} // client tries to update status

	s.PrepareForUpdate(context.Background(), new, old)

	if new.Status.Phase != v1alpha1.TaskPhaseRunning {
		t.Errorf("client status leaked through non-status update: %v", new.Status.Phase)
	}
	if new.Status.ObservedGeneration != 5 {
		t.Errorf("observedGeneration leaked: %d", new.Status.ObservedGeneration)
	}
}

func TestStrategy_PrepareForUpdate_BumpsGenerationOnSpecChange(t *testing.T) {
	s := NewStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 3
	new := old.DeepCopy()
	new.Spec.Image = "alpine:edge"

	s.PrepareForUpdate(context.Background(), new, old)

	if new.Generation != 4 {
		t.Errorf("generation = %d; want 4 after spec change", new.Generation)
	}
}

func TestStrategy_PrepareForUpdate_KeepsGenerationOnUnchangedSpec(t *testing.T) {
	s := NewStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 3
	new := old.DeepCopy()
	new.ObjectMeta.Labels = map[string]string{"k": "v"} // metadata-only change

	s.PrepareForUpdate(context.Background(), new, old)

	if new.Generation != 3 {
		t.Errorf("generation = %d; want 3 (unchanged spec)", new.Generation)
	}
}

func TestStrategy_PrepareForUpdate_TreatsNilAndEmptySliceAsEqual(t *testing.T) {
	s := NewStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 7
	old.Spec.Args = nil

	new := old.DeepCopy()
	new.Spec.Args = []string{} // JSON round-trip artifact

	s.PrepareForUpdate(context.Background(), new, old)

	if new.Generation != 7 {
		t.Errorf("generation = %d; want 7 (nil vs empty slice should not bump)", new.Generation)
	}
}

func TestStrategy_Validate_RequiresImage(t *testing.T) {
	s := NewStrategy(newScheme(t))
	task := validTask("t1")
	task.Spec.Image = ""

	errs := s.Validate(context.Background(), task)
	if !hasFieldError(errs, "spec.image") {
		t.Errorf("expected validation error on spec.image; got %v", errs)
	}
}

func TestStrategy_Validate_AcceptsValidTask(t *testing.T) {
	s := NewStrategy(newScheme(t))
	if errs := s.Validate(context.Background(), validTask("t1")); len(errs) != 0 {
		t.Errorf("valid task rejected: %v", errs)
	}
}

func TestStrategy_Validate_RejectsBadName(t *testing.T) {
	s := NewStrategy(newScheme(t))
	task := validTask("UPPERCASE_NOT_DNS")
	if errs := s.Validate(context.Background(), task); !hasFieldError(errs, "metadata.name") {
		t.Errorf("expected metadata.name error; got %v", errs)
	}
}

func TestStrategy_ValidateUpdate_AllowsImageChange(t *testing.T) {
	s := NewStrategy(newScheme(t))
	old := validTask("t1")
	new := old.DeepCopy()
	new.Spec.Image = "alpine:edge"

	if errs := s.ValidateUpdate(context.Background(), new, old); len(errs) != 0 {
		t.Errorf("image change rejected: %v", errs)
	}
}

func TestStatusStrategy_PrepareForUpdate_RevertsSpecChange(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 4
	new := old.DeepCopy()
	new.Spec.Image = "evil:latest"
	new.Status.Phase = v1alpha1.TaskPhaseRunning

	s.PrepareForUpdate(context.Background(), new, old)

	if new.Spec.Image != old.Spec.Image {
		t.Errorf("spec.image leaked through /status: got %q", new.Spec.Image)
	}
	if new.Generation != old.Generation {
		t.Errorf("generation leaked through /status: got %d", new.Generation)
	}
	if new.Status.Phase != v1alpha1.TaskPhaseRunning {
		t.Errorf("status.phase did not flow through /status: got %q", new.Status.Phase)
	}
}

func TestStatusStrategy_ValidateUpdate_AcceptsObservedGenerationEqualToGeneration(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 5
	new := old.DeepCopy()
	new.Status.ObservedGeneration = 5

	if errs := s.ValidateUpdate(context.Background(), new, old); len(errs) != 0 {
		t.Errorf("observedGeneration == generation rejected: %v", errs)
	}
}

func TestStatusStrategy_ValidateUpdate_RejectsObservedGenerationAheadOfGeneration(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 3
	new := old.DeepCopy()
	new.Status.ObservedGeneration = 99 // controller cannot have observed a future generation

	errs := s.ValidateUpdate(context.Background(), new, old)
	if !hasFieldError(errs, "status.observedGeneration") {
		t.Errorf("expected status.observedGeneration error; got %v", errs)
	}
}

func TestStatusStrategy_ValidateUpdate_RejectsNegativeObservedGeneration(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 5
	new := old.DeepCopy()
	new.Status.ObservedGeneration = -1

	errs := s.ValidateUpdate(context.Background(), new, old)
	if !hasFieldError(errs, "status.observedGeneration") {
		t.Errorf("expected negative observedGeneration error; got %v", errs)
	}
}

func TestStatusStrategy_ValidateUpdate_RejectsConditionMissingType(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 1
	new := old.DeepCopy()
	new.Status.Conditions = []metav1.Condition{{
		// Type intentionally missing — metav1validation requires it.
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "Whatever",
	}}

	errs := s.ValidateUpdate(context.Background(), new, old)
	if !hasFieldError(errs, "status.conditions") {
		t.Errorf("expected status.conditions error; got %v", errs)
	}
}

func TestStatusStrategy_ValidateUpdate_AcceptsValidConditions(t *testing.T) {
	s := NewStatusStrategy(newScheme(t))
	old := validTask("t1")
	old.Generation = 1
	new := old.DeepCopy()
	new.Status.Conditions = []metav1.Condition{{
		Type:               v1alpha1.TaskConditionReady,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Reason:             "Started",
		Message:            "task is running",
	}}

	if errs := s.ValidateUpdate(context.Background(), new, old); len(errs) != 0 {
		t.Errorf("valid conditions rejected: %v", errs)
	}
}

// hasFieldError reports whether errs contains any error whose Field path
// has the given prefix. Tests assert on the path so that this stays
// resilient to phrasing changes in error Detail strings.
func hasFieldError(errs field.ErrorList, fieldPathPrefix string) bool {
	for _, e := range errs {
		if e.Field == fieldPathPrefix ||
			strings.HasPrefix(e.Field, fieldPathPrefix+".") ||
			strings.HasPrefix(e.Field, fieldPathPrefix+"[") {
			return true
		}
	}
	return false
}
