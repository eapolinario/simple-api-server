/*
Copyright 2026 Eduardo Apolinario.
*/

package main

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	apiservercompatibility "k8s.io/apiserver/pkg/util/compatibility"

	"github.com/eapolinario/simple-api-server/pkg/apiserver"
)

// AuthorizationModeAlwaysAllow short-circuits authz with an "allow
// everything" authorizer. Loopback-bind + local-dev only — see
// Options.AuthorizationMode for the full rationale.
const AuthorizationModeAlwaysAllow = "AlwaysAllow"

// Options aggregates the option groups simple-apiserver consumes from
// k8s.io/apiserver/pkg/server/options. We do NOT use
// genericoptions.RecommendedOptions — its ApplyTo unconditionally calls
// Etcd.ApplyTo, and there is no etcd backend (storage is in-memory).
// Composing the groups directly is the documented escape hatch and makes
// the "no etcd" architectural commitment a build-time fact rather than a
// runtime hope.
//
// Auth posture matches the project's architectural commitment: both
// authn and authz delegate to the host kube-apiserver in production
// (the normal aggregated-apiserver deployment shape). In-cluster lookup
// failures are non-fatal so the binary can start outside a cluster for
// flag inspection / local experimentation — but no permissive fallback
// is wired in. Permissive auth lives only in the in-process test harness.
type Options struct {
	SecureServing  *genericoptions.SecureServingOptionsWithLoopback
	Authentication *genericoptions.DelegatingAuthenticationOptions
	Authorization  *genericoptions.DelegatingAuthorizationOptions
	Audit          *genericoptions.AuditOptions
	Features       *genericoptions.FeatureOptions

	// AuthorizationMode toggles the authz path. Empty (default) keeps
	// the delegating authorizer wired by Authorization.ApplyTo — the
	// production posture. The only other accepted value is
	// "AlwaysAllow", which replaces the authorizer with one that
	// approves every request. AlwaysAllow exists for local development
	// and the in-process test harness; it MUST NOT be used on a
	// non-loopback interface. The `just dev` recipe pairs it with
	// --bind-address=127.0.0.1 to enforce that at the network layer.
	AuthorizationMode string

	// stdout / stderr are wired through so tests can capture output.
	StdOut io.Writer
	StdErr io.Writer
}

// NewOptions returns Options populated with k8s.io/apiserver defaults
// plus the local-friendly tweaks described in the type comment.
func NewOptions(out, errOut io.Writer) *Options {
	o := &Options{
		SecureServing:  genericoptions.NewSecureServingOptions().WithLoopback(),
		Authentication: genericoptions.NewDelegatingAuthenticationOptions(),
		Authorization:  genericoptions.NewDelegatingAuthorizationOptions(),
		Audit:          genericoptions.NewAuditOptions(),
		Features:       genericoptions.NewFeatureOptions(),
		StdOut:         out,
		StdErr:         errOut,
	}

	// 6443 is conventional for an aggregated apiserver pod and matches
	// what kube-apiserver expects when proxying through the APIService.
	o.SecureServing.BindPort = 6443

	// Delegating auth normally fetches extension-apiserver-authentication
	// from kube-system. Mark these as non-fatal so the binary can boot
	// without a kubeconfig — useful for `--help`, smoke tests, and
	// running inside a cluster where the lookup happens via the SA
	// token. Real requests will still 401/403 without proper config.
	o.Authentication.RemoteKubeConfigFileOptional = true
	o.Authentication.TolerateInClusterLookupFailure = true
	o.Authorization.RemoteKubeConfigFileOptional = true

	// Priority & Fairness requires a kube clientset (FlowcontrolV1).
	// We have no clientset to pass, and APF is orthogonal to the
	// aggregation experiment — disable by default. Re-enable explicitly
	// with --enable-priority-and-fairness=true if a clientset is ever
	// supplied.
	o.Features.EnablePriorityAndFairness = false

	return o
}

// AddFlags wires every option group's flags onto fs.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.SecureServing.AddFlags(fs)
	o.Authentication.AddFlags(fs)
	o.Authorization.AddFlags(fs)
	o.Audit.AddFlags(fs)
	o.Features.AddFlags(fs)
	fs.StringVar(&o.AuthorizationMode, "authorization-mode", o.AuthorizationMode,
		`Authorization mode. Empty (default) delegates to the host kube-apiserver via SubjectAccessReview. `+
			`"AlwaysAllow" bypasses authz entirely and is intended for local development on a loopback bind only.`)
}

// Validate aggregates per-option-group validation errors.
func (o *Options) Validate() error {
	var errs []error
	errs = append(errs, o.SecureServing.Validate()...)
	errs = append(errs, o.Authentication.Validate()...)
	errs = append(errs, o.Authorization.Validate()...)
	errs = append(errs, o.Audit.Validate()...)
	errs = append(errs, o.Features.Validate()...)
	switch o.AuthorizationMode {
	case "", AuthorizationModeAlwaysAllow:
	default:
		errs = append(errs, fmt.Errorf("--authorization-mode=%q is not supported (allowed: \"\", %q)",
			o.AuthorizationMode, AuthorizationModeAlwaysAllow))
	}
	return utilerrors.NewAggregate(errs)
}

// Complete resolves derived configuration. Today its only job is to
// generate a self-signed cert/key pair into --cert-dir if the user did
// not supply --tls-cert-file / --tls-private-key-file. The cert acts as
// its own CA for kubectl trust purposes (--certificate-authority=...).
func (o *Options) Complete() error {
	loopback := []net.IP{net.ParseIP("127.0.0.1")}
	return o.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, loopback)
}

// Config builds an apiserver.Config from these Options. It mirrors the
// body of genericoptions.RecommendedOptions.ApplyTo minus the etcd,
// egress-selector, traces, admission, and core-api steps — none of
// which we use. CoreAPI is intentionally skipped: it requires either an
// explicit kubeconfig or a successful rest.InClusterConfig() call, and
// supplying a SharedInformerFactory for core types brings no value when
// no admission plugin consumes it.
func (o *Options) Config() (*apiserver.Config, error) {
	serverConfig := genericapiserver.NewRecommendedConfig(apiserver.Codecs)
	serverConfig.OpenAPIConfig = apiserver.DefaultOpenAPIConfig()
	serverConfig.OpenAPIV3Config = apiserver.DefaultOpenAPIV3Config()

	// EffectiveVersion drives OpenAPI emulation-version selection and
	// admission plugin compatibility checks. Config.Complete dereferences
	// it unconditionally, so it must be set before ApplyTo / Complete.
	// DefaultBuildEffectiveVersion pins it to whatever k8s.io/apiserver
	// vendored version is in go.mod — the right answer for an aggregated
	// apiserver that has no independent compatibility story.
	serverConfig.EffectiveVersion = apiservercompatibility.DefaultBuildEffectiveVersion()

	if err := o.SecureServing.ApplyTo(&serverConfig.Config.SecureServing, &serverConfig.Config.LoopbackClientConfig); err != nil {
		return nil, fmt.Errorf("apply secure serving: %w", err)
	}
	if err := o.Authentication.ApplyTo(&serverConfig.Config.Authentication, serverConfig.Config.SecureServing, serverConfig.OpenAPIConfig); err != nil {
		return nil, fmt.Errorf("apply authentication: %w", err)
	}
	switch o.AuthorizationMode {
	case AuthorizationModeAlwaysAllow:
		// Skip Authorization.ApplyTo entirely — it would try to dial a
		// SubjectAccessReview endpoint we don't have. The path-based
		// AlwaysAllowPaths option also can't help here because it
		// short-circuits non-resource requests only (see
		// k8s.io/apiserver/pkg/authorization/path), so resource calls
		// like /apis/tasks.example.com/v1alpha1/.../tasks would still
		// be denied. Wiring a real always-allow authorizer is the only
		// way to make CRUD work for an anonymous local kubectl.
		serverConfig.Config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()
	default:
		if err := o.Authorization.ApplyTo(&serverConfig.Config.Authorization); err != nil {
			return nil, fmt.Errorf("apply authorization: %w", err)
		}
	}
	if err := o.Audit.ApplyTo(&serverConfig.Config); err != nil {
		return nil, fmt.Errorf("apply audit: %w", err)
	}
	// nil clientset / nil informers are explicitly tolerated by
	// FeatureOptions.ApplyTo when EnablePriorityAndFairness=false, which
	// is our default. See NewOptions.
	if err := o.Features.ApplyTo(&serverConfig.Config, nil, nil); err != nil {
		return nil, fmt.Errorf("apply features: %w", err)
	}

	return &apiserver.Config{
		GenericConfig: serverConfig,
		ExtraConfig:   apiserver.ExtraConfig{},
	}, nil
}

// NewCommand builds the root cobra command for the simple-apiserver
// binary. out/errOut are wired through so tests can capture output.
func NewCommand(out, errOut io.Writer) *cobra.Command {
	o := NewOptions(out, errOut)

	cmd := &cobra.Command{
		Use:   "simple-apiserver",
		Short: "Aggregated apiserver serving tasks.example.com",
		Long: `Aggregated apiserver for the tasks.example.com/v1alpha1 API group.

Storage is in-memory; server restart wipes all state. Watch is deferred
and returns HTTP 405.`,
		// SilenceUsage avoids dumping --help on every runtime error; the
		// flags themselves are well-documented and a stack of usage text
		// after a "kubeconfig not found" is just noise.
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := o.Complete(); err != nil {
				return err
			}
			if err := o.Validate(); err != nil {
				return err
			}
			return o.Run(c.Context())
		},
	}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	o.AddFlags(cmd.Flags())
	return cmd
}

// Run builds and starts the server, blocking until ctx is canceled.
// When ctx is nil (binary entrypoint) a signal-handling context from
// k8s.io/apiserver is used so SIGINT / SIGTERM trigger graceful shutdown.
func (o *Options) Run(ctx context.Context) error {
	config, err := o.Config()
	if err != nil {
		return err
	}
	server, err := config.Complete().New()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = genericapiserver.SetupSignalContext()
	}
	return server.GenericAPIServer.PrepareRun().Run(ctx.Done())
}
