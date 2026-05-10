/*
Copyright 2026 Eduardo Apolinario.
*/

// Package v1alpha1 contains the external (versioned) Task API types served
// at tasks.example.com/v1alpha1.
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TaskPhase is the high-level lifecycle phase of a Task.
type TaskPhase string

const (
	TaskPhasePending   TaskPhase = "Pending"
	TaskPhaseRunning   TaskPhase = "Running"
	TaskPhaseSucceeded TaskPhase = "Succeeded"
	TaskPhaseFailed    TaskPhase = "Failed"
)

// Standard condition types reported on TaskStatus.Conditions.
const (
	TaskConditionReady    = "Ready"
	TaskConditionFinished = "Finished"
)

// TaskSpec is the user-facing intent for a Task.
type TaskSpec struct {
	// Image is the container image to run. Required.
	Image string `json:"image"`

	// Command optionally overrides the image entrypoint.
	// +optional
	Command []string `json:"command,omitempty"`

	// Args optionally overrides the image command arguments.
	// +optional
	Args []string `json:"args,omitempty"`
}

// TaskStatus reports the observed state of a Task. Only mutable via the
// /status subresource — never via plain updates to the resource.
type TaskStatus struct {
	// Phase is a high-level summary of where the Task is in its lifecycle.
	// +optional
	Phase TaskPhase `json:"phase,omitempty"`

	// Conditions are detailed status reports using the standard
	// metav1.Condition shape (Type, Status, Reason, Message,
	// LastTransitionTime, ObservedGeneration).
	// +optional
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the .metadata.generation last reconciled by a
	// controller. Status updates must set this to track lag between
	// user intent and observed state.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Task is a unit of asynchronous work scheduled through this aggregated
// APIServer.
type Task struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the user-supplied desired state.
	Spec TaskSpec `json:"spec,omitempty"`

	// Status is the observed state. Only writable via the /status subresource.
	// +optional
	Status TaskStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskList is a list of Task objects.
type TaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Task `json:"items"`
}
