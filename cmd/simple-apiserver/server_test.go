/*
Copyright 2026 Eduardo Apolinario.
*/

package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestNewOptions_Defaults checks that the values we explicitly tweak in
// NewOptions are actually applied. A regression here would mean either
// APF accidentally re-enabled (would 500 on every request — no clientset)
// or the bind port silently drifted off 6443 (would break the canonical
// APIService manifest before integration tests catch it).
func TestNewOptions_Defaults(t *testing.T) {
	t.Parallel()

	o := NewOptions(io.Discard, io.Discard)

	if o.SecureServing.BindPort != 6443 {
		t.Errorf("BindPort = %d; want 6443", o.SecureServing.BindPort)
	}
	if o.Features.EnablePriorityAndFairness {
		t.Error("EnablePriorityAndFairness should default to false (no clientset wired)")
	}
	if !o.Authentication.RemoteKubeConfigFileOptional {
		t.Error("Authentication.RemoteKubeConfigFileOptional should default to true")
	}
	if !o.Authentication.TolerateInClusterLookupFailure {
		t.Error("Authentication.TolerateInClusterLookupFailure should default to true")
	}
	if !o.Authorization.RemoteKubeConfigFileOptional {
		t.Error("Authorization.RemoteKubeConfigFileOptional should default to true")
	}
}

// TestAddFlags_RegistersExpectedFlags spot-checks that AddFlags wires
// the option groups we claim to expose. It guards against an option
// group being silently dropped during a refactor — the symptom in
// production would be a flag the user passes being ignored.
func TestAddFlags_RegistersExpectedFlags(t *testing.T) {
	t.Parallel()

	o := NewOptions(io.Discard, io.Discard)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	o.AddFlags(fs)

	// One flag per option group, picked because it's the canonical
	// entry point users reach for first.
	want := []string{
		"secure-port",                  // SecureServing
		"authentication-kubeconfig",    // DelegatingAuthentication
		"authorization-kubeconfig",     // DelegatingAuthorization
		"audit-log-path",               // Audit
		"enable-priority-and-fairness", // Features
	}
	for _, name := range want {
		if fs.Lookup(name) == nil {
			t.Errorf("flag %q not registered", name)
		}
	}
}

// TestCommand_Help guards against a startup-time crash: cobra panics if
// flags collide between groups, and --help executes far less of the
// option machinery than --version-style boot paths do. Running --help
// without panic confirms NewCommand can at least be constructed.
func TestCommand_Help(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	cmd := NewCommand(&stdout, &stderr)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(stdout.String(), "simple-apiserver") {
		t.Errorf("--help stdout missing usage line; got:\n%s", stdout.String())
	}
}

// TestRun_ExitsOnCanceledContext is a minimal lifecycle check: pass an
// already-canceled context, expect Run to return (not hang). It avoids
// binding a real port by tripping out before the listener starts — a
// canceled stop channel is not part of Run's contract, but Complete +
// Config can still fail fast enough to make this test useful by
// requiring an invalid cert-dir.
//
// Today this test only asserts that Run returns an error when
// SecureServing options can't be applied (cert-dir cannot be created).
// A real "boot the server in-process and shut it down" test belongs in
// the integration-harness todo.
func TestRun_ReturnsErrorOnBadCertDir(t *testing.T) {
	t.Parallel()

	o := NewOptions(io.Discard, io.Discard)
	// "/" is read-only for non-root; MaybeDefaultWithSelfSignedCerts
	// inside Complete will fail to mkdir there.
	o.SecureServing.ServerCert.CertDirectory = "/dev/null/cannot-create"

	if err := o.Complete(); err == nil {
		t.Fatal("Complete() should fail with an unwriteable cert-dir")
	}

	// Belt-and-braces: even if Complete somehow succeeded, Run must not
	// hang when the context is already done.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := o.Run(ctx); err == nil {
		t.Error("Run with canceled context returned nil; expected an error or fast exit")
	}
}
