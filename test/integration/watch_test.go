/*
Copyright 2026 Eduardo Apolinario.
*/

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Watch is a deferred feature: the REST layer in pkg/registry/tasks/task
// returns apierrors.NewMethodNotSupported, which the aggregator surfaces
// as HTTP 405 with a metav1.Status body. The tests in this file lock
// that contract from two angles — raw HTTP and the typed client-go
// error — so that anyone "just adding a minimal watch" trips them.
//
// When watch is implemented for real (see the architectural commitment
// in the repo Copilot instructions), this file should be deleted in
// the same PR that introduces working watch semantics.

// TestWatch_ReturnsHTTP405_OverWire goes around client-go to confirm
// the actual wire response. A 200 with EOF, or a 5xx, would mean watch
// is half-wired in a way client-go might paper over.
func TestWatch_ReturnsHTTP405_OverWire(t *testing.T) {
	t.Parallel()
	cfg := StartTestServer(t)

	hc := httpClientFor(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := cfg.Host + "/apis/tasks.example.com/v1alpha1/namespaces/default/tasks?watch=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("do watch request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%q; want 405", resp.StatusCode, string(body))
	}

	var status metav1.Status
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode metav1.Status body: %v", err)
	}
	if status.Status != metav1.StatusFailure {
		t.Errorf("status.status=%q; want %q", status.Status, metav1.StatusFailure)
	}
	if status.Reason != metav1.StatusReasonMethodNotAllowed {
		t.Errorf("status.reason=%q; want %q", status.Reason, metav1.StatusReasonMethodNotAllowed)
	}
	if status.Code != http.StatusMethodNotAllowed {
		t.Errorf("status.code=%d; want %d", status.Code, http.StatusMethodNotAllowed)
	}
}

// TestWatch_SurfacesAsTypedMethodNotSupported confirms the deferral is
// observable through normal client-go usage too — controllers using
// SharedInformerFactory would otherwise hit a confusing transport
// error instead of the typed apierrors.IsMethodNotSupported signal.
func TestWatch_SurfacesAsTypedMethodNotSupported(t *testing.T) {
	t.Parallel()
	cfg := StartTestServer(t)

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build dynamic client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	w, err := dc.Resource(tasksGVR).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err == nil {
		w.Stop()
		t.Fatal("Watch succeeded; want MethodNotSupported until watch is implemented")
	}
	if !apierrors.IsMethodNotSupported(err) {
		t.Errorf("Watch error type: %v; want MethodNotSupported", err)
	}
}

// httpClientFor builds an http.Client that trusts the harness's
// self-signed serving cert. Lives in the test file rather than in
// framework.go because it is only useful for tests that intentionally
// step outside the typed client-go surface.
func httpClientFor(t *testing.T, cfg *rest.Config) *http.Client {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cfg.TLSClientConfig.CAData) {
		t.Fatal("append CAData to pool")
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
		Timeout: 5 * time.Second,
	}
}
