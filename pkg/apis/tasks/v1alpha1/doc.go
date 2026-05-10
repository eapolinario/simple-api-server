/*
Copyright 2026 Eduardo Apolinario.
*/

// Package-level code-generation tags. These must live in a standalone
// comment block (not attached to the package doc) so deepcopy-gen picks
// them up.

// +k8s:deepcopy-gen=package
// +groupName=tasks.example.com

package v1alpha1
