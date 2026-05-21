//go:build e2e

/*
Copyright 2026 Eduardo Apolinario.
*/

// Package e2e exercises the aggregated apiserver through the
// kube-aggregator in a real (kind) cluster. The cluster is provisioned
// by hack/e2e-up.sh — these tests do not stand up infrastructure
// themselves.
//
// Target selection is intentionally strict to avoid the footgun where
// a stale ~/.kube/config silently routes the test at the wrong
// cluster:
//
//  1. KUBECONFIG must be explicitly set. We never fall back to
//     ~/.kube/config. `just test-e2e` sets it to the file
//     hack/e2e-up.sh writes; running `go test -tags=e2e ./test/e2e/...`
//     from a bare shell will SKIP, not silently re-use whatever
//     cluster happens to be in scope.
//
//  2. Once KUBECONFIG is set, the test PRE-FLIGHTS that the
//     v1alpha1.tasks.example.com APIService exists and has
//     condition Available=True. A missing or unavailable APIService
//     is a hard FAIL — not a skip — because by then the dev has
//     explicitly opted in.
//
// Gated by the `e2e` build tag so the default `just test` loop never
// pays for them. Run with `just test-e2e` after `just e2e-up`.
package e2e

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
)

// tasksGVR mirrors the integration-test constant. Duplicated rather
// than shared because the e2e package is build-tagged and importing it
// from integration would force integration to inherit the tag too.
var tasksGVR = schema.GroupVersionResource{
	Group:    v1alpha1.GroupName,
	Version:  v1alpha1.SchemeGroupVersion.Version,
	Resource: "tasks",
}

// apiServicesGVR identifies the kube-aggregator's own APIService
// resource — the test uses it to preflight that our group is actually
// registered and Available on whichever cluster KUBECONFIG points at.
var apiServicesGVR = schema.GroupVersionResource{
	Group:    "apiregistration.k8s.io",
	Version:  "v1",
	Resource: "apiservices",
}

const apiServiceName = "v1alpha1.tasks.example.com"

// preflightOnce guards the per-cluster APIService preflight so a test
// run with N test functions hits the kube-apiserver once, not N times.
var (
	preflightOnce sync.Once
	preflightErr  error
)

// e2eClient resolves a dynamic client against the cluster pointed at
// by $KUBECONFIG (no fallback to ~/.kube/config — see package doc) and
// runs the APIService preflight on first call.
//
// Returns a namespace-scoped client for tasks/default plus a context
// with a 30s deadline.
func e2eClient(t *testing.T) (context.Context, dynamic.ResourceInterface) {
	t.Helper()

	kc := os.Getenv("KUBECONFIG")
	if kc == "" {
		t.Skip("KUBECONFIG not set; use `just test-e2e` (which sets it) after `just e2e-up`. " +
			"Refusing to fall back to ~/.kube/config — that footgun was the whole point of this fix.")
	}
	if _, err := os.Stat(kc); err != nil {
		t.Skipf("KUBECONFIG=%s does not exist (run `just e2e-up` first): %v", kc, err)
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kc)
	if err != nil {
		t.Fatalf("load kubeconfig %s: %v", kc, err)
	}

	preflightOnce.Do(func() { preflightErr = preflightAPIService(cfg) })
	if preflightErr != nil {
		t.Fatalf("e2e preflight against KUBECONFIG=%s: %v", kc, preflightErr)
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build dynamic client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	return ctx, dc.Resource(tasksGVR).Namespace("default")
}

// preflightAPIService asserts that the cluster pointed at by cfg has
// our APIService registered AND that the aggregator considers it
// Available. This is what makes the "wrong cluster" failure mode loud:
// even if some other cluster happens to answer the dynamic client, it
// won't have THIS APIService.
func preflightAPIService(cfg *rest.Config) error {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	obj, err := dc.Resource(apiServicesGVR).Get(ctx, apiServiceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("APIService %s is not registered on this cluster — "+
				"either KUBECONFIG points at the wrong cluster, or `just e2e-up` "+
				"hasn't been run", apiServiceName)
		}
		return fmt.Errorf("get APIService %s: %w", apiServiceName, err)
	}

	conds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("APIService %s: read status.conditions: %w", apiServiceName, err)
	}
	if !found {
		return fmt.Errorf("APIService %s has no status.conditions yet — aggregator hasn't probed it", apiServiceName)
	}

	for _, raw := range conds {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ctype, _ := c["type"].(string)
		if ctype != "Available" {
			continue
		}
		status, _ := c["status"].(string)
		if status == "True" {
			return nil
		}
		reason, _ := c["reason"].(string)
		msg, _ := c["message"].(string)
		return fmt.Errorf("APIService %s Available=%s reason=%s message=%s",
			apiServiceName, status, reason, msg)
	}
	return fmt.Errorf("APIService %s has no Available condition (aggregator hasn't probed it)", apiServiceName)
}

// newTask is the e2e mirror of the integration helper of the same
// name. Kept local for the same reason as tasksGVR.
func newTask(name, image string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": v1alpha1.SchemeGroupVersion.String(),
			"kind":       "Task",
			"metadata": map[string]any{
				"name":      name,
				"namespace": "default",
			},
			"spec": map[string]any{
				"image": image,
			},
		},
	}
}

// TestAggregatorRoundTrip is the one assertion this skeleton needs to
// make: a Task created through the cluster's kube-apiserver round-trips
// through the aggregator to the simple-apiserver pod and back. If the
// APIService isn't registered, the Service selector is wrong, the
// container is crashlooping, or RBAC blocks the front-proxy, this test
// fails. Anything more granular belongs in follow-up e2e tests.
func TestAggregatorRoundTrip(t *testing.T) {
	ctx, client := e2eClient(t)

	const taskName = "e2e-roundtrip"

	// Defensive cleanup in case a previous run left state behind.
	// Ignore NotFound; anything else is a real failure we want surfaced.
	_ = client.Delete(ctx, taskName, metav1.DeleteOptions{})

	created, err := client.Create(ctx, newTask(taskName, "registry.example.com/busybox:1.0"), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Task through aggregator: %v", err)
	}
	if created.GetName() != taskName {
		t.Fatalf("created.Name = %q; want %q", created.GetName(), taskName)
	}
	if created.GetUID() == "" {
		t.Fatal("created.UID is empty; aggregator returned an object the server never persisted")
	}

	// Ensure we always clean up, even if a later assertion fails.
	t.Cleanup(func() {
		if err := client.Delete(context.Background(), taskName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("cleanup: delete %s: %v", taskName, err)
		}
	})

	got, err := client.Get(ctx, taskName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Task through aggregator: %v", err)
	}
	if got.GetUID() != created.GetUID() {
		t.Fatalf("get.UID = %q; want %q (different object came back)", got.GetUID(), created.GetUID())
	}

	image, found, err := unstructured.NestedString(got.Object, "spec", "image")
	if err != nil || !found {
		t.Fatalf("spec.image missing from round-tripped object: found=%v err=%v", found, err)
	}
	if image != "registry.example.com/busybox:1.0" {
		t.Errorf("spec.image = %q; want registry.example.com/busybox:1.0", image)
	}

	if err := client.Delete(ctx, taskName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete Task through aggregator: %v", err)
	}

	if _, err := client.Get(ctx, taskName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("get after delete: err = %v; want NotFound", err)
	}
}

// TestMain logs the KUBECONFIG once so failures in CI are debuggable
// without re-running with extra verbosity. Cheap and worth the noise.
func TestMain(m *testing.M) {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		_, _ = os.Stderr.WriteString("e2e: KUBECONFIG=" + kc + "\n")
	} else {
		_, _ = os.Stderr.WriteString("e2e: KUBECONFIG not set; all tests will SKIP. Run `just e2e-up && just test-e2e`.\n")
	}
	os.Exit(m.Run())
}
