/*
Copyright 2026 Eduardo Apolinario.
*/

// Package install registers the tasks.example.com API group's external
// versions into a runtime.Scheme. There is no internal version today;
// if conversion is ever added, register internal types and adjust
// SetVersionPriority here.
package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

// Install adds every served version of tasks.example.com to scheme and
// sets the version priority. Panics on registration failure — every
// caller wires this into a process-global Scheme at startup, and a
// failure here is unrecoverable.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(v1alpha1.SchemeGroupVersion))
}
