/*
Copyright 2026 Eduardo Apolinario.
*/

// Package apiserver wires the GenericAPIServer for simple-api-server. It
// owns the runtime.Scheme, the CodecFactory, the ParameterCodec, and
// assembles an APIGroupInfo that mounts tasks.example.com/v1alpha1
// (resource + /status subresource) backed by a single in-memory Store.
//
// The shape follows the canonical k8s.io/sample-apiserver pattern:
// Config → CompletedConfig → APIServer. Callers (cmd/simple-apiserver,
// integration tests) supply a genericapiserver.RecommendedConfig with
// serving / auth / OpenAPI configured, then call Complete().New().
//
// Watch is deferred. The REST layer returns NewMethodNotSupported on
// Watch; the aggregator surfaces that as HTTP 405. See
// pkg/registry/tasks/task and the watch-405-test todo.
package apiserver

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	openapicommon "k8s.io/kube-openapi/pkg/common"

	tasksinstall "github.com/eapolinario/simple-api-server/pkg/apis/tasks/install"
	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
	generatedopenapi "github.com/eapolinario/simple-api-server/pkg/generated/openapi"
	"github.com/eapolinario/simple-api-server/pkg/registry/tasks/task"
	"github.com/eapolinario/simple-api-server/pkg/storage/inmemory"
)

// ServerName is reported on generic endpoints (/version, log lines,
// etc.) and must match the binary name in cmd/simple-apiserver.
const ServerName = "simple-apiserver"

var (
	// Scheme is the runtime.Scheme used by every internal component:
	// REST codecs, the strategy's ObjectTyper, and the ParameterCodec.
	// Populated in init() — never mutate after process start.
	Scheme = runtime.NewScheme()

	// Codecs is the CodecFactory backing request/response serialization.
	Codecs = serializer.NewCodecFactory(Scheme)

	// ParameterCodec decodes URL query parameters (ListOptions,
	// GetOptions, …) into their typed forms.
	ParameterCodec = runtime.NewParameterCodec(Scheme)
)

func init() {
	tasksinstall.Install(Scheme)

	// Register metav1 group/version so generic kube types (Status,
	// APIGroup, ListOptions, …) can round-trip alongside our group.
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}

// ExtraConfig holds simple-apiserver-specific configuration that is not
// covered by genericapiserver.RecommendedConfig. Empty today; carried
// so the Config / CompletedConfig pattern is wired and ready for future
// additions (custom feature gates, etc.) without churn at callsites.
type ExtraConfig struct{}

// Config is the top-level configuration for an APIServer.
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

// APIServer is the assembled, runnable server.
type APIServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig is a fully-resolved Config. The wrapping struct exists
// so callers cannot accidentally pass an incomplete Config to New().
type CompletedConfig struct {
	*completedConfig
}

// Complete fills in defaults and resolves derived configuration. Call
// exactly once. After Complete, GenericConfig must not be mutated.
func (c *Config) Complete() CompletedConfig {
	completed := completedConfig{
		GenericConfig: c.GenericConfig.Complete(),
		ExtraConfig:   &c.ExtraConfig,
	}
	return CompletedConfig{&completed}
}

// New builds an APIServer from the completed config. It registers the
// tasks.example.com/v1alpha1 group (resource + /status subresource)
// backed by a freshly-allocated in-memory Store.
func (c CompletedConfig) New() (*APIServer, error) {
	genericServer, err := c.GenericConfig.New(ServerName, genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}
	s := &APIServer{GenericAPIServer: genericServer}

	if err := s.installTasksGroup(); err != nil {
		return nil, err
	}
	return s, nil
}

// installTasksGroup constructs and registers the APIGroupInfo for
// tasks.example.com. It allocates one *inmemory.Store shared by REST
// and StatusREST so spec and status writes hit the same object.
func (s *APIServer) installTasksGroup() error {
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(v1alpha1.GroupName, Scheme, ParameterCodec, Codecs)

	gr := v1alpha1.Resource("tasks")
	store := inmemory.New(gr)
	strategy := task.NewStrategy(Scheme)
	statusStrategy := task.NewStatusStrategy(Scheme)

	storage := map[string]rest.Storage{
		"tasks":        task.NewREST(store, strategy, gr),
		"tasks/status": task.NewStatusREST(store, statusStrategy, gr),
	}
	apiGroupInfo.VersionedResourcesStorageMap[v1alpha1.SchemeGroupVersion.Version] = storage

	return s.GenericAPIServer.InstallAPIGroup(&apiGroupInfo)
}

// DefaultOpenAPIConfig returns an OpenAPI v2 config wired to the
// generated GetOpenAPIDefinitions. Callers plug it into
// RecommendedConfig.OpenAPIConfig before Complete().
func DefaultOpenAPIConfig() *openapicommon.Config {
	cfg := genericapiserver.DefaultOpenAPIConfig(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(Scheme))
	cfg.Info.Title = ServerName
	cfg.Info.Version = v1alpha1.SchemeGroupVersion.Version
	return cfg
}

// DefaultOpenAPIV3Config returns an OpenAPI v3 config wired to the
// generated GetOpenAPIDefinitions.
func DefaultOpenAPIV3Config() *openapicommon.OpenAPIV3Config {
	cfg := genericapiserver.DefaultOpenAPIV3Config(generatedopenapi.GetOpenAPIDefinitions, openapinamer.NewDefinitionNamer(Scheme))
	cfg.Info.Title = ServerName
	cfg.Info.Version = v1alpha1.SchemeGroupVersion.Version
	return cfg
}
