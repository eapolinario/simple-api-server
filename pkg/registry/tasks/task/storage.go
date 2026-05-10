/*
Copyright 2026 Eduardo Apolinario.
*/

// Package task wires the Task type into k8s.io/apiserver's REST machinery.
//
// REST handles spec mutations (create / list / get / update / delete);
// StatusREST handles the /status subresource. Both REST and StatusREST
// share a single *inmemory.Store — the spec and status subresources are
// two views over the same object.
//
// Watch is deferred. Watch() returns NewMethodNotSupported so the
// aggregator surfaces 405; HTTP-level coverage lives in the
// watch-405-test todo.
package task

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/registry/rest"

	v1alpha1 "github.com/eapolinario/simple-api-server/pkg/apis/tasks/v1alpha1"
	"github.com/eapolinario/simple-api-server/pkg/storage/inmemory"
)

// Compile-time assertions that REST and StatusREST satisfy the rest
// interfaces the aggregator cares about. Watch is included so we get a
// build break the day someone removes the explicit 405 by accident.
var (
	_ rest.Storage              = &REST{}
	_ rest.Scoper               = &REST{}
	_ rest.Getter               = &REST{}
	_ rest.Lister               = &REST{}
	_ rest.Creater              = &REST{}
	_ rest.Updater              = &REST{}
	_ rest.Patcher              = &REST{}
	_ rest.GracefulDeleter      = &REST{}
	_ rest.Watcher              = &REST{}
	_ rest.SingularNameProvider = &REST{}
	_ rest.ShortNamesProvider   = &REST{}
	_ rest.CategoriesProvider   = &REST{}
	_ rest.TableConvertor       = &REST{}

	_ rest.Storage = &StatusREST{}
	_ rest.Getter  = &StatusREST{}
	_ rest.Updater = &StatusREST{}
	_ rest.Patcher = &StatusREST{}
)

// REST handles RESTful operations on the Task resource (spec view).
type REST struct {
	store    *inmemory.Store
	strategy Strategy
	gr       schema.GroupResource

	table rest.TableConvertor
}

// NewREST returns a REST backed by an in-memory store. The same store is
// also wrapped by NewStatusREST so that spec and status updates affect
// the same persisted object.
func NewREST(store *inmemory.Store, strategy Strategy, gr schema.GroupResource) *REST {
	return &REST{
		store:    store,
		strategy: strategy,
		gr:       gr,
		table:    rest.NewDefaultTableConvertor(gr),
	}
}

// New returns an empty Task for use as a decode target.
func (r *REST) New() runtime.Object { return &v1alpha1.Task{} }

// NewList returns an empty TaskList for use as a decode target.
func (r *REST) NewList() runtime.Object { return &v1alpha1.TaskList{} }

// Destroy releases any resources held by the REST. The in-memory store
// owns nothing external; this is a no-op.
func (r *REST) Destroy() {}

// NamespaceScoped reports that Task is namespaced.
func (r *REST) NamespaceScoped() bool { return r.strategy.NamespaceScoped() }

// GetSingularName supports `kubectl get task` (vs. `tasks`).
func (r *REST) GetSingularName() string { return "task" }

// ShortNames lets `kubectl get tk` resolve to Task.
func (r *REST) ShortNames() []string { return []string{"tk"} }

// Categories lets `kubectl get all` include Tasks. Add carefully — `all`
// is the only well-known category and is meant for user-facing primary
// workload resources.
func (r *REST) Categories() []string { return []string{"all"} }

// ConvertToTable produces the tabular response kubectl renders. The
// default convertor emits Name + Age columns; richer Task-specific
// columns (Phase, Image) can land in a follow-up without touching
// callers.
func (r *REST) ConvertToTable(ctx context.Context, obj runtime.Object, opts runtime.Object) (*metav1.Table, error) {
	return r.table.ConvertToTable(ctx, obj, opts)
}

// Get returns the named Task in the request's namespace.
func (r *REST) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	key, err := keyFromContext(ctx, name, r.NamespaceScoped(), r.gr)
	if err != nil {
		return nil, err
	}
	return r.store.Get(key)
}

// List returns the Tasks in the request's namespace (or across all
// namespaces if the request was made cluster-scoped). Only the label
// selector is honoured today; field selectors return InternalError.
func (r *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	if options != nil && options.FieldSelector != nil && !options.FieldSelector.Empty() {
		return nil, apierrors.NewInternalError(fmt.Errorf("field selectors are not supported on %s", r.gr.String()))
	}

	selector := labels.Everything()
	if options != nil && options.LabelSelector != nil {
		selector = options.LabelSelector
	}

	ns := namespaceFromContext(ctx, r.NamespaceScoped())
	raw, err := r.store.List(ns)
	if err != nil {
		return nil, err
	}

	list := &v1alpha1.TaskList{Items: make([]v1alpha1.Task, 0, len(raw))}
	var maxRV uint64
	for _, obj := range raw {
		t, ok := obj.(*v1alpha1.Task)
		if !ok {
			return nil, apierrors.NewInternalError(fmt.Errorf("stored object is not a *Task: %T", obj))
		}
		if !selector.Matches(labels.Set(t.Labels)) {
			continue
		}
		list.Items = append(list.Items, *t)
		if rv := parseRVOrZero(t.ResourceVersion); rv > maxRV {
			maxRV = rv
		}
	}
	if maxRV == 0 {
		maxRV = r.store.CurrentResourceVersion()
	}
	list.ResourceVersion = formatRV(maxRV)
	return list, nil
}

// Create persists a new Task after running strategy + admission checks.
func (r *REST) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, _ *metav1.CreateOptions) (runtime.Object, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return nil, apierrors.NewInternalError(err)
	}
	rest.FillObjectMetaSystemFields(accessor)

	if err := rest.BeforeCreate(r.strategy, ctx, obj); err != nil {
		return nil, err
	}
	if createValidation != nil {
		if err := createValidation(ctx, obj.DeepCopyObject()); err != nil {
			return nil, err
		}
	}

	key := inmemory.Key{Namespace: accessor.GetNamespace(), Name: accessor.GetName()}
	return r.store.Create(key, obj)
}

// Update finds the named Task, applies the client-supplied changes through
// the strategy, and persists. AllowCreateOnUpdate is false: PUT to a
// missing name returns NotFound rather than implicitly creating.
func (r *REST) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	return updateOnStore(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate, options, r.store, r.strategy, r.strategy, r.gr)
}

// Delete removes the named Task, honouring resourceVersion / UID
// preconditions when supplied.
func (r *REST) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	key, err := keyFromContext(ctx, name, r.NamespaceScoped(), r.gr)
	if err != nil {
		return nil, false, err
	}

	existing, err := r.store.Get(key)
	if err != nil {
		return nil, false, err
	}

	if err := checkPreconditions(existing, options.Preconditions, r.gr, name); err != nil {
		return nil, false, err
	}
	if deleteValidation != nil {
		if err := deleteValidation(ctx, existing.DeepCopyObject()); err != nil {
			return nil, false, err
		}
	}

	deleted, err := r.store.Delete(key)
	if err != nil {
		return nil, false, err
	}
	return deleted, true, nil
}

// Watch is deliberately not implemented — see package doc and the
// watch-405-test todo. The aggregator turns NewMethodNotSupported into
// HTTP 405, which is the contract our clients can rely on.
func (r *REST) Watch(_ context.Context, _ *metainternalversion.ListOptions) (watch.Interface, error) {
	return nil, apierrors.NewMethodNotSupported(r.gr, "watch")
}

// StatusREST handles the /status subresource. It shares the underlying
// store with REST so that status updates and spec updates affect the
// same object; the strategy difference is what gates which fields each
// view is allowed to mutate.
type StatusREST struct {
	store    *inmemory.Store
	strategy StatusStrategy
	gr       schema.GroupResource
}

// NewStatusREST wraps store with the status-subresource strategy.
func NewStatusREST(store *inmemory.Store, strategy StatusStrategy, gr schema.GroupResource) *StatusREST {
	return &StatusREST{store: store, strategy: strategy, gr: gr}
}

// New returns an empty Task for use as a decode target.
func (r *StatusREST) New() runtime.Object { return &v1alpha1.Task{} }

// Destroy is a no-op; the underlying store is owned by REST.
func (r *StatusREST) Destroy() {}

// Get on a status subresource returns the full object — clients see the
// same shape as on the main resource.
func (r *StatusREST) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	key, err := keyFromContext(ctx, name, r.strategy.NamespaceScoped(), r.gr)
	if err != nil {
		return nil, err
	}
	return r.store.Get(key)
}

// Update on /status routes through the StatusStrategy, which pins spec
// and generation back to the old object so status writers cannot mutate
// user intent.
func (r *StatusREST) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	// The status subresource never creates; the createStrategy passed to
	// updateOnStore is the main Strategy, but forceAllowCreate=false plus
	// AllowCreateOnUpdate=false means it is never invoked.
	return updateOnStore(ctx, name, objInfo, createValidation, updateValidation, forceAllowCreate, options, r.store, r.strategy.Strategy, r.strategy, r.gr)
}

// updateOnStore is the shared Update implementation used by both REST and
// StatusREST. createStrategy is invoked only on the create-on-update path
// (which is currently unreachable because AllowCreateOnUpdate=false);
// updateStrategy is the variant that actually shapes mutations.
//
// We keep both paths in one function so the conflict / preconditions /
// admission ordering is identical between spec and status writes.
func updateOnStore(
	ctx context.Context,
	name string,
	objInfo rest.UpdatedObjectInfo,
	createValidation rest.ValidateObjectFunc,
	updateValidation rest.ValidateObjectUpdateFunc,
	forceAllowCreate bool,
	_ *metav1.UpdateOptions,
	store *inmemory.Store,
	createStrategy Strategy,
	updateStrategy rest.RESTUpdateStrategy,
	gr schema.GroupResource,
) (runtime.Object, bool, error) {
	key, err := keyFromContext(ctx, name, updateStrategy.NamespaceScoped(), gr)
	if err != nil {
		return nil, false, err
	}

	existing, getErr := store.Get(key)
	if apierrors.IsNotFound(getErr) {
		if !forceAllowCreate && !createStrategy.AllowCreateOnUpdate() {
			return nil, false, getErr
		}
		newObj, err := objInfo.UpdatedObject(ctx, createStrategy.New())
		if err != nil {
			return nil, false, err
		}
		acc, err := meta.Accessor(newObj)
		if err != nil {
			return nil, false, apierrors.NewInternalError(err)
		}
		rest.FillObjectMetaSystemFields(acc)
		if err := rest.BeforeCreate(createStrategy, ctx, newObj); err != nil {
			return nil, false, err
		}
		if createValidation != nil {
			if err := createValidation(ctx, newObj.DeepCopyObject()); err != nil {
				return nil, false, err
			}
		}
		created, err := store.Create(key, newObj)
		if err != nil {
			return nil, false, err
		}
		return created, true, nil
	}
	if getErr != nil {
		return nil, false, getErr
	}

	newObj, err := objInfo.UpdatedObject(ctx, existing.DeepCopyObject())
	if err != nil {
		return nil, false, err
	}

	if err := checkPreconditions(existing, objInfo.Preconditions(), gr, name); err != nil {
		return nil, false, err
	}
	if err := checkResourceVersion(existing, newObj, updateStrategy, gr); err != nil {
		return nil, false, err
	}

	if err := rest.BeforeUpdate(updateStrategy, ctx, newObj, existing); err != nil {
		return nil, false, err
	}
	if updateValidation != nil {
		if err := updateValidation(ctx, newObj.DeepCopyObject(), existing.DeepCopyObject()); err != nil {
			return nil, false, err
		}
	}

	updated, err := store.Update(key, newObj)
	if err != nil {
		return nil, false, err
	}
	return updated, false, nil
}

// New is a Strategy helper used only on the create-on-update path.
func (s Strategy) New() runtime.Object { return &v1alpha1.Task{} }

// keyFromContext extracts the (namespace, name) tuple from the request
// context. For namespaced resources the namespace must be present; for
// cluster-scoped resources it must be empty.
func keyFromContext(ctx context.Context, name string, namespaced bool, gr schema.GroupResource) (inmemory.Key, error) {
	if name == "" {
		return inmemory.Key{}, apierrors.NewBadRequest("name is required")
	}
	ns := namespaceFromContext(ctx, namespaced)
	if namespaced && ns == "" {
		return inmemory.Key{}, apierrors.NewBadRequest(fmt.Sprintf("namespace is required for %s", gr.String()))
	}
	return inmemory.Key{Namespace: ns, Name: name}, nil
}

// checkResourceVersion enforces optimistic concurrency. With
// AllowUnconditionalUpdate=false an empty client RV is treated as a
// stale read and rejected with Conflict so clients re-fetch and retry.
func checkResourceVersion(existing, updated runtime.Object, strategy rest.RESTUpdateStrategy, gr schema.GroupResource) error {
	oldAcc, err := meta.Accessor(existing)
	if err != nil {
		return apierrors.NewInternalError(err)
	}
	newAcc, err := meta.Accessor(updated)
	if err != nil {
		return apierrors.NewInternalError(err)
	}

	newRV := newAcc.GetResourceVersion()
	if newRV == "" {
		if strategy.AllowUnconditionalUpdate() {
			return nil
		}
		return apierrors.NewConflict(gr, newAcc.GetName(),
			fmt.Errorf("resourceVersion must be specified for an update"))
	}
	if newRV != oldAcc.GetResourceVersion() {
		return apierrors.NewConflict(gr, newAcc.GetName(),
			fmt.Errorf("the object has been modified; please apply your changes to the latest version and try again"))
	}
	return nil
}

// checkPreconditions enforces UID and ResourceVersion preconditions
// supplied via DELETE options or UpdatedObjectInfo.Preconditions.
func checkPreconditions(existing runtime.Object, pre *metav1.Preconditions, gr schema.GroupResource, name string) error {
	if pre == nil {
		return nil
	}
	acc, err := meta.Accessor(existing)
	if err != nil {
		return apierrors.NewInternalError(err)
	}
	if pre.UID != nil && *pre.UID != acc.GetUID() {
		return apierrors.NewConflict(gr, name,
			fmt.Errorf("UID precondition (%s) does not match current UID (%s)", *pre.UID, acc.GetUID()))
	}
	if pre.ResourceVersion != nil && *pre.ResourceVersion != acc.GetResourceVersion() {
		return apierrors.NewConflict(gr, name,
			fmt.Errorf("resourceVersion precondition (%s) does not match current resourceVersion (%s)", *pre.ResourceVersion, acc.GetResourceVersion()))
	}
	return nil
}

// namespaceFromContext returns the request namespace; empty string for
// cluster-scoped requests or when the context carries no namespace
// information (used by List across all namespaces).
func namespaceFromContext(ctx context.Context, _ bool) string {
	ns, _ := requestNamespace(ctx)
	return ns
}
