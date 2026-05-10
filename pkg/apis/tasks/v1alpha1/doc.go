/*
Copyright 2026 Eduardo Apolinario.
*/

// Package-level code-generation tags. These must live in a standalone
// comment block (not attached to the package doc) so deepcopy-gen and
// openapi-gen pick them up.

// +k8s:deepcopy-gen=package
// +k8s:openapi-gen=true
// +groupName=tasks.example.com

package v1alpha1
