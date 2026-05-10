/*
Copyright 2026 Eduardo Apolinario.
*/

package task

import (
	"context"
	"fmt"

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

// Strategy implements the create / non-status-update behavior for Task.
//
// The method set conforms to k8s.io/apiserver/pkg/registry/rest's
// RESTCreateStrategy and RESTUpdateStrategy without importing that
// package — that wiring lives in the registry-storage layer
// (pkg/registry/tasks/task/storage.go, registry-storage todo). Keeping
// the strategy free of the apiserver dep means strategy tests are pure
// unit tests with no HTTP, no scheme registration beyond v1alpha1, and
// no k8s.io/apiserver imports.
//
// Concurrency: Strategy values are stateless and safe for concurrent use.
type Strategy struct {
	runtime.ObjectTyper
}

// NewStrategy returns a Strategy that uses typer (typically a runtime
// Scheme) for ObjectTyper duties.
func NewStrategy(typer runtime.ObjectTyper) Strategy {
	return Strategy{ObjectTyper: typer}
}

// NamespaceScoped reports that Task is a namespaced resource.
func (Strategy) NamespaceScoped() bool { return true }

// AllowCreateOnUpdate is false: PUT to a non-existent name returns
// NotFound rather than implicitly creating.
func (Strategy) AllowCreateOnUpdate() bool { return false }

// AllowUnconditionalUpdate is false: clients must supply a matching
// resourceVersion on update so we can detect concurrent modification.
func (Strategy) AllowUnconditionalUpdate() bool { return false }

// Canonicalize is a no-op. We do not normalize whitespace, sort maps, or
// otherwise rewrite client input.
func (Strategy) Canonicalize(_ runtime.Object) {}

// PrepareForCreate runs before a Task is persisted. Status is owned by
// the /status subresource, so any client-supplied status is dropped here.
// Generation always starts at 1; metadata.generation is server-managed.
func (Strategy) PrepareForCreate(_ context.Context, obj runtime.Object) {
	task := obj.(*v1alpha1.Task)
	task.Status = v1alpha1.TaskStatus{}
	task.Generation = 1
}

// PrepareForUpdate runs before a non-status update is persisted. It
// preserves the existing status (status mutations only flow through the
// /status subresource) and bumps generation iff spec changed.
func (Strategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newTask := obj.(*v1alpha1.Task)
	oldTask := old.(*v1alpha1.Task)

	newTask.Status = oldTask.Status

	if specEqual(&newTask.Spec, &oldTask.Spec) {
		newTask.Generation = oldTask.Generation
	} else {
		newTask.Generation = oldTask.Generation + 1
	}
}

// Validate runs full validation on a newly created Task.
func (Strategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	return validateTask(obj.(*v1alpha1.Task))
}

// ValidateUpdate runs validation on a non-status update.
func (Strategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newTask := obj.(*v1alpha1.Task)
	oldTask := old.(*v1alpha1.Task)

	errs := validateTask(newTask)
	errs = append(errs, validateImmutableFields(newTask, oldTask)...)
	return errs
}

// WarningsOnCreate currently returns nil. Hook for future deprecation /
// best-practice nudges.
func (Strategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string { return nil }

// WarningsOnUpdate currently returns nil.
func (Strategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string { return nil }

// StatusStrategy implements the /status subresource semantics. Spec
// mutations and generation changes are silently reverted; status
// mutations go through.
//
// Embeds Strategy so it inherits NamespaceScoped, AllowCreateOnUpdate,
// AllowUnconditionalUpdate, Canonicalize, etc., and only overrides the
// methods that differ.
type StatusStrategy struct {
	Strategy
}

// NewStatusStrategy mirrors NewStrategy but returns the /status variant.
func NewStatusStrategy(typer runtime.ObjectTyper) StatusStrategy {
	return StatusStrategy{Strategy: NewStrategy(typer)}
}

// PrepareForUpdate on the /status subresource pins spec and generation
// to their previous values, so a controller patching /status cannot
// accidentally mutate user intent.
func (StatusStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newTask := obj.(*v1alpha1.Task)
	oldTask := old.(*v1alpha1.Task)

	newTask.Spec = oldTask.Spec
	newTask.Generation = oldTask.Generation
}

// ValidateUpdate runs status-specific validation: observedGeneration
// discipline and standard condition shape.
func (StatusStrategy) ValidateUpdate(_ context.Context, obj, old runtime.Object) field.ErrorList {
	newTask := obj.(*v1alpha1.Task)
	return validateStatusUpdate(newTask)
}

// validateTask checks ObjectMeta and required spec fields.
func validateTask(task *v1alpha1.Task) field.ErrorList {
	errs := apivalidation.ValidateObjectMeta(
		&task.ObjectMeta,
		true, // namespaced
		apivalidation.NameIsDNSSubdomain,
		field.NewPath("metadata"),
	)

	specPath := field.NewPath("spec")
	if task.Spec.Image == "" {
		errs = append(errs, field.Required(specPath.Child("image"), "image is required"))
	}
	return errs
}

// validateImmutableFields enforces fields that may not change once set.
// Currently empty — no immutable spec fields. Add here as the API grows.
func validateImmutableFields(_, _ *v1alpha1.Task) field.ErrorList {
	return nil
}

// validateStatusUpdate enforces the observedGeneration contract and the
// standard metav1.Condition shape. Called from StatusStrategy where
// generation is already pinned to its old value, so referring to
// task.Generation is safe.
func validateStatusUpdate(task *v1alpha1.Task) field.ErrorList {
	var errs field.ErrorList
	statusPath := field.NewPath("status")

	og := task.Status.ObservedGeneration
	switch {
	case og < 0:
		errs = append(errs, field.Invalid(
			statusPath.Child("observedGeneration"),
			og,
			"observedGeneration must be non-negative",
		))
	case og > task.Generation:
		errs = append(errs, field.Invalid(
			statusPath.Child("observedGeneration"),
			og,
			fmt.Sprintf("observedGeneration (%d) must not exceed generation (%d)", og, task.Generation),
		))
	}

	errs = append(errs,
		metav1validation.ValidateConditions(
			task.Status.Conditions,
			statusPath.Child("conditions"),
		)...,
	)

	return errs
}

// specEqual avoids reflect.DeepEqual to sidestep the nil-vs-empty-slice
// ambiguity that surfaces after JSON round-trips.
func specEqual(a, b *v1alpha1.TaskSpec) bool {
	return a.Image == b.Image &&
		stringSliceEqual(a.Command, b.Command) &&
		stringSliceEqual(a.Args, b.Args)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
