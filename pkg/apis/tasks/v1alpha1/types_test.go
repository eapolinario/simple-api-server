/*
Copyright 2026 Eduardo Apolinario.
*/

package v1alpha1

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestTask_DeepCopy_IsolatesNestedSlices(t *testing.T) {
	orig := &Task{
		ObjectMeta: metav1.ObjectMeta{Name: "t1", Namespace: "ns"},
		Spec: TaskSpec{
			Image:   "alpine:3",
			Command: []string{"sh", "-c"},
			Args:    []string{"echo hi"},
		},
		Status: TaskStatus{
			Phase: TaskPhaseRunning,
			Conditions: []metav1.Condition{{
				Type:    TaskConditionReady,
				Status:  metav1.ConditionTrue,
				Reason:  "Started",
				Message: "running",
			}},
			ObservedGeneration: 7,
		},
	}

	cp := orig.DeepCopy()
	if cp == orig {
		t.Fatal("DeepCopy returned the same pointer")
	}

	// Mutate every slice field through the copy and verify the original is
	// untouched. This catches accidental shallow copies of nested slices.
	cp.Spec.Command[0] = "MUTATED"
	cp.Spec.Args[0] = "MUTATED"
	cp.Status.Conditions[0].Reason = "MUTATED"

	if orig.Spec.Command[0] != "sh" {
		t.Errorf("Spec.Command leaked through DeepCopy: got %q", orig.Spec.Command[0])
	}
	if orig.Spec.Args[0] != "echo hi" {
		t.Errorf("Spec.Args leaked through DeepCopy: got %q", orig.Spec.Args[0])
	}
	if orig.Status.Conditions[0].Reason != "Started" {
		t.Errorf("Status.Conditions leaked through DeepCopy: got %q", orig.Status.Conditions[0].Reason)
	}
}

func TestTask_DeepCopyObject_ImplementsRuntimeObject(t *testing.T) {
	var _ runtime.Object = &Task{}
	var _ runtime.Object = &TaskList{}

	in := &Task{Spec: TaskSpec{Image: "alpine"}}
	out, ok := in.DeepCopyObject().(*Task)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *Task", in.DeepCopyObject())
	}
	if !reflect.DeepEqual(in.Spec, out.Spec) {
		t.Errorf("DeepCopyObject lost Spec: in=%+v out=%+v", in.Spec, out.Spec)
	}
}

func TestTaskList_DeepCopy_CopiesItems(t *testing.T) {
	orig := &TaskList{
		Items: []Task{
			{Spec: TaskSpec{Image: "a"}},
			{Spec: TaskSpec{Image: "b"}},
		},
	}
	cp := orig.DeepCopy()
	cp.Items[0].Spec.Image = "MUTATED"
	if orig.Items[0].Spec.Image != "a" {
		t.Errorf("TaskList.Items leaked through DeepCopy: got %q", orig.Items[0].Spec.Image)
	}
}

func TestResource_QualifiedWithGroup(t *testing.T) {
	got := Resource("tasks").String()
	want := "tasks.tasks.example.com"
	if got != want {
		t.Errorf("Resource(%q) = %q; want %q", "tasks", got, want)
	}
}

func TestSchemeGroupVersion_Stable(t *testing.T) {
	if SchemeGroupVersion.Group != "tasks.example.com" {
		t.Errorf("Group = %q; want tasks.example.com", SchemeGroupVersion.Group)
	}
	if SchemeGroupVersion.Version != "v1alpha1" {
		t.Errorf("Version = %q; want v1alpha1", SchemeGroupVersion.Version)
	}
}

func TestAddToScheme_RegistersTaskAndTaskList(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	for _, kind := range []string{"Task", "TaskList"} {
		gvk := SchemeGroupVersion.WithKind(kind)
		if _, err := scheme.New(gvk); err != nil {
			t.Errorf("scheme.New(%v): %v", gvk, err)
		}
	}
}
