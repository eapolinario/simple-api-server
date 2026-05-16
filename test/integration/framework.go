/*
Copyright 2026 Eduardo Apolinario.
*/

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/apiserver/pkg/authentication/request/anonymous"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	apiservercompatibility "k8s.io/apiserver/pkg/util/compatibility"
	"k8s.io/client-go/rest"

	"github.com/eapolinario/simple-api-server/pkg/apiserver"
)

// StartTestServer boots a simple-apiserver in-process on a loopback port
// and returns a *rest.Config wired to talk to it. The server is torn
// down via t.Cleanup, so callers don't need to manage lifetime.
//
// Posture matches the project's "permissive in tests" commitment from
// the repo instructions: authentication tags every request as
// system:anonymous and authorization is AlwaysAllow. This MUST stay
// confined to integration tests — see cmd/simple-apiserver/server.go
// for how production wires DelegatingAuthentication / Authorization.
//
// The function blocks until /healthz returns 200, so tests can begin
// issuing requests immediately on return.
func StartTestServer(t *testing.T) *rest.Config {
	t.Helper()

	// Reserve a free loopback port by binding ourselves and letting
	// SecureServing adopt the listener. SecureServing.Listener takes
	// precedence over BindPort/BindAddress in ApplyTo, so this avoids
	// the "pick a port, hope it's still free a few ms later" race that
	// plagues the bind-then-set-port approach.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	certDir := t.TempDir()
	so := genericoptions.NewSecureServingOptions().WithLoopback()
	so.ServerCert.CertDirectory = certDir
	so.Listener = listener
	so.BindAddress = net.ParseIP("127.0.0.1")
	so.BindPort = port

	// Generate the self-signed serving cert into certDir. The cert
	// doubles as its own CA — same trick as `just dev`.
	if err := so.MaybeDefaultWithSelfSignedCerts(
		"localhost",
		nil,
		[]net.IP{net.ParseIP("127.0.0.1")},
	); err != nil {
		t.Fatalf("generate self-signed cert: %v", err)
	}

	serverCfg := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	serverCfg.OpenAPIConfig = apiserver.DefaultOpenAPIConfig()
	serverCfg.OpenAPIV3Config = apiserver.DefaultOpenAPIV3Config()
	// Config.Complete dereferences EffectiveVersion unconditionally
	// when wiring OpenAPI. See cmd/simple-apiserver/server.go for the
	// full rationale.
	serverCfg.EffectiveVersion = apiservercompatibility.DefaultBuildEffectiveVersion()

	if err := so.ApplyTo(&serverCfg.Config.SecureServing, &serverCfg.Config.LoopbackClientConfig); err != nil {
		t.Fatalf("apply secure serving: %v", err)
	}

	// Anonymous + AlwaysAllow lets tests skip the entire token /
	// kubeconfig dance. Real auth is exercised end-to-end by the
	// kind-based e2e suite, not here.
	serverCfg.Config.Authentication.Authenticator = anonymous.NewAuthenticator(nil)
	serverCfg.Config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

	apiCfg := &apiserver.Config{GenericConfig: serverCfg}
	server, err := apiCfg.Complete().New()
	if err != nil {
		t.Fatalf("build apiserver: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	prepared := server.GenericAPIServer.PrepareRun()
	runErr := make(chan error, 1)
	go func() {
		runErr <- prepared.Run(ctx.Done())
	}()

	// Read back the on-disk cert. apiserver.crt acts as the CA for the
	// rest.Config we hand to the test — same as kubectl with
	// --certificate-authority=. Using CAData rather than CAFile keeps
	// the test self-contained even if certDir is GCed mid-run.
	caData, err := os.ReadFile(filepath.Join(certDir, "apiserver.crt"))
	if err != nil {
		t.Fatalf("read serving cert: %v", err)
	}

	cfg := &rest.Config{
		Host: fmt.Sprintf("https://127.0.0.1:%d", port),
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caData,
		},
	}

	if err := waitForHealthz(ctx, cfg.Host, caData, 5*time.Second); err != nil {
		select {
		case rErr := <-runErr:
			t.Fatalf("server exited before healthz returned 200: %v (waitErr=%v)", rErr, err)
		default:
		}
		t.Fatalf("healthz never became ready: %v", err)
	}

	return cfg
}

// waitForHealthz polls https://host/healthz until it returns 200 or
// timeout elapses. The CA pool is built from caData (the self-signed
// serving cert).
func waitForHealthz(ctx context.Context, host string, caData []byte, timeout time.Duration) error {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caData) {
		return fmt.Errorf("append serving cert to pool")
	}
	hc := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
		Timeout: 1 * time.Second,
	}

	deadline := time.Now().Add(timeout)
	url := host + "/healthz"
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := hc.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("healthz: last error %v", err)
			}
			return fmt.Errorf("healthz: last status %d", resp.StatusCode)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
