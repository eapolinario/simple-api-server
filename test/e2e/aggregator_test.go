//go:build e2e

/*
Copyright 2026 Eduardo Apolinario.
*/

// Package e2e exercises the aggregated apiserver through the
// kube-aggregator in a real (kind) cluster. The cluster is provisioned
// by hack/e2e-up.sh — these tests do not stand up infrastructure
// themselves; they just assume KUBECONFIG points at a cluster where the
// v1alpha1.tasks.example.com APIService is registered and Available.
//
// Gated by the `e2e` build tag so the default `just test` loop never
// pays for them. Run with `just test-e2e` after `just e2e-up`.
package e2e

import (
	"context"
	"os"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
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

// e2eClient resolves a dynamic client against the cluster pointed at by
// KUBECONFIG (falling back to the standard kubeconfig loading rules:
// $KUBECONFIG, then ~/.kube/config). Tests that can't reach a cluster
// are skipped, not failed — the kind cluster is provisioned by
// hack/e2e-up.sh and may legitimately be absent on a dev box.
func e2eClient(t *testing.T) (context.Context, dynamic.ResourceInterface) {
	t.Helper()

	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader, &clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		t.Skipf("no usable kubeconfig (run hack/e2e-up.sh first): %v", err)
	}

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("build dynamic client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	return ctx, dc.Resource(tasksGVR).Namespace("default")
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
		// Stderr so it shows up next to test output regardless of -v.
		_, _ = os.Stderr.WriteString("e2e: KUBECONFIG=" + kc + "\n")
	}
	os.Exit(m.Run())
}
