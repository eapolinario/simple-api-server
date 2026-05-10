/*
Copyright 2026 Eduardo Apolinario.
*/

package apiserver

import (
	"net/url"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

// TestSchemeRegistersTaskTypes confirms that the package-level init()
// installed tasks.example.com/v1alpha1.{Task,TaskList} into Scheme.
// Without this the REST layer would 500 on every request.
func TestSchemeRegistersTaskTypes(t *testing.T) {
	t.Parallel()

	gv := v1alpha1.SchemeGroupVersion
	for _, kind := range []string{"Task", "TaskList"} {
		gvk := gv.WithKind(kind)
		if _, err := Scheme.New(gvk); err != nil {
			t.Errorf("Scheme.New(%s) = %v; want non-error", gvk, err)
		}
	}
}

// TestSchemeRegistersMetaV1 ensures metav1 types share the Scheme. The
// generic apiserver needs Status to encode error responses and
// APIGroupList to serve discovery.
func TestSchemeRegistersMetaV1(t *testing.T) {
	t.Parallel()

	metaGV := schema.GroupVersion{Version: "v1"}
	for _, kind := range []string{"Status", "APIGroup", "APIGroupList", "APIResourceList", "APIVersions"} {
		gvk := metaGV.WithKind(kind)
		if _, err := Scheme.New(gvk); err != nil {
			t.Errorf("Scheme.New(%s) = %v; want non-error", gvk, err)
		}
	}
}

// TestParameterCodecDecodesListOptions guards the request-handler
// contract: the generic apiserver decodes ?labelSelector= and friends
// through our ParameterCodec, and metav1.ListOptions must be reachable
// from it. A regression here would surface as "no kind ListOptions is
// registered" at request time.
func TestParameterCodecDecodesListOptions(t *testing.T) {
	t.Parallel()

	q := url.Values{
		"labelSelector":   []string{"app=task"},
		"resourceVersion": []string{"42"},
	}

	var opts metav1.ListOptions
	if err := ParameterCodec.DecodeParameters(q, schema.GroupVersion{Version: "v1"}, &opts); err != nil {
		t.Fatalf("DecodeParameters: %v", err)
	}
	if opts.LabelSelector != "app=task" {
		t.Errorf("LabelSelector = %q; want %q", opts.LabelSelector, "app=task")
	}
	if opts.ResourceVersion != "42" {
		t.Errorf("ResourceVersion = %q; want %q", opts.ResourceVersion, "42")
	}
}

// TestDefaultOpenAPIConfig_PopulatesInfo checks that the v2 OpenAPI
// config returned to callers is non-nil and has Title/Version
// populated. The aggregator advertises this Info to clients.
func TestDefaultOpenAPIConfig_PopulatesInfo(t *testing.T) {
	t.Parallel()

	cfg := DefaultOpenAPIConfig()
	if cfg == nil {
		t.Fatal("DefaultOpenAPIConfig() = nil")
	}
	if cfg.Info == nil {
		t.Fatal("cfg.Info = nil")
	}
	if cfg.Info.Title != ServerName {
		t.Errorf("Info.Title = %q; want %q", cfg.Info.Title, ServerName)
	}
	if cfg.Info.Version != v1alpha1.SchemeGroupVersion.Version {
		t.Errorf("Info.Version = %q; want %q", cfg.Info.Version, v1alpha1.SchemeGroupVersion.Version)
	}
	if cfg.GetDefinitions == nil {
		t.Error("cfg.GetDefinitions is nil; OpenAPI definitions not wired")
	}
}

// TestDefaultOpenAPIV3Config_PopulatesInfo mirrors the v2 test for v3.
func TestDefaultOpenAPIV3Config_PopulatesInfo(t *testing.T) {
	t.Parallel()

	cfg := DefaultOpenAPIV3Config()
	if cfg == nil {
		t.Fatal("DefaultOpenAPIV3Config() = nil")
	}
	if cfg.Info == nil {
		t.Fatal("cfg.Info = nil")
	}
	if cfg.Info.Title != ServerName {
		t.Errorf("Info.Title = %q; want %q", cfg.Info.Title, ServerName)
	}
	if cfg.Info.Version != v1alpha1.SchemeGroupVersion.Version {
		t.Errorf("Info.Version = %q; want %q", cfg.Info.Version, v1alpha1.SchemeGroupVersion.Version)
	}
	if cfg.GetDefinitions == nil {
		t.Error("cfg.GetDefinitions is nil; OpenAPI definitions not wired")
	}
}
