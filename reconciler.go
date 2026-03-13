package kopper

import (
	gocontext "context"
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/context"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/lo"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Custom Resources that uses "status" subresource
// must implement this interface.
type StatusPatchGenerator interface {
	GenerateStatusPatch(previousState runtime.Object) client.Patch
}

// OnUpsertFunc is a function that is called when a resource is created or updated
type OnUpsertFunc[PT client.Object] func(context.Context, PT) error

// OnDeleteFunc is a function that is called when a resource is deleted
type OnDeleteFunc func(context.Context, string) error

// OnConflictFunc is called when a CRD already exists in the database with a different uid.
// It is responsible in identifying the corresponding existing record as PT & deleting it
// so the new resource can be created.
type OnConflictFunc[PT client.Object] func(context.Context, PT) error

func SetupReconciler[T any, PT interface {
	*T
	client.Object
}](ctx context.Context, mgr ctrl.Manager, onUpsert OnUpsertFunc[PT], onDelete OnDeleteFunc, onConflict OnConflictFunc[PT], finalizer string) (Reconciler[T, PT], error) {
	if finalizer == "" {
		return Reconciler[T, PT]{}, fmt.Errorf("field Finalizer cannot be empty")
	}

	r := Reconciler[T, PT]{
		DutyContext:    ctx,
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		OnUpsertFunc:   onUpsert,
		OnDeleteFunc:   onDelete,
		OnConflictFunc: onConflict,
		Finalizer:      finalizer,
		Events:         mgr.GetEventRecorderFor(finalizer),
	}

	if err := r.SetupWithManager(mgr); err != nil {
		return Reconciler[T, PT]{}, fmt.Errorf("error setting up manager: %w", err)
	}

	return r, nil
}

type Reconciler[T any, PT interface {
	*T
	client.Object
}] struct {
	client.Client
	DutyContext    context.Context
	Scheme         *runtime.Scheme
	OnUpsertFunc   OnUpsertFunc[PT]
	OnDeleteFunc   OnDeleteFunc
	OnConflictFunc OnConflictFunc[PT]
	Finalizer      string
	Events         record.EventRecorder
	gvk            schema.GroupVersionKind
}

func (r *Reconciler[T, PT]) Reconcile(ctx gocontext.Context, req ctrl.Request) (ctrl.Result, error) {
	raw := &unstructured.Unstructured{}
	raw.SetGroupVersionKind(r.gvk)

	if err := r.Get(ctx, req.NamespacedName, raw); err != nil {
		if apiErrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	resourceName := fmt.Sprintf("%s[%s/%s:%s]", r.gvk.Kind, req.Namespace, req.Name, raw.GetUID())

	obj := PT(new(T))
	if err := fromUnstructured(raw.Object, obj); err != nil {
		logger.Errorf("[kopper] malformed resource %s: %v", resourceName, err)
		r.Events.Event(raw, "Warning", "MalformedResource",
			fmt.Sprintf("Resource spec does not match expected schema: %v", err))
		return ctrl.Result{}, fmt.Errorf("failed to convert unstructured to typed object: %w", err)
	}

	original := obj.DeepCopyObject()

	if !obj.GetDeletionTimestamp().IsZero() {
		logger.V(2).Infof("[kopper] deleting %s", resourceName)
		if err := r.OnDeleteFunc(r.DutyContext, string(obj.GetUID())); err != nil {
			logger.Errorf("[kopper] failed to delete %s: %v", resourceName, err)
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
		controllerutil.RemoveFinalizer(obj, r.Finalizer)
		r.Events.Event(obj, "Normal", "Deleted", fmt.Sprintf("Deleted %s", resourceName))
		return ctrl.Result{}, r.Update(ctx, obj)
	}

	isCreated := false
	if !controllerutil.ContainsFinalizer(obj, r.Finalizer) {
		controllerutil.AddFinalizer(obj, r.Finalizer)
		if err := r.Update(ctx, obj); err != nil {
			logger.Errorf("[kopper] failed to update finalizers %s: %v", resourceName, err)
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
		isCreated = true
	}

	if err := r.OnUpsertFunc(r.DutyContext, obj); err != nil {
		if isUniqueConstraintError(err) && r.OnConflictFunc != nil {
			logger.V(2).Infof("[kopper] deleting %s due to unique constraint violation", resourceName)

			if err := r.OnConflictFunc(r.DutyContext, obj); err != nil {
				logger.Errorf("[kopper] failed to delete %s: %v", resourceName, err)
				return ctrl.Result{Requeue: true, RequeueAfter: time.Minute * 5}, err
			}

			// after successful deletion, retry after a short delay
			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 15}, err
		}

		logger.Errorf("[kopper] failed to upsert %s: %v", resourceName, err)
		return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
	}

	if mgr, ok := any(obj).(StatusPatchGenerator); ok {
		if patch := mgr.GenerateStatusPatch(original); patch != nil {
			if err := r.Status().Patch(r.DutyContext, obj, patch); err != nil {
				logger.Errorf("[kopper] failed to update status %s: %v", resourceName, err)
				return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
			}
		}
	} else {
		// TODO: only for backward compatibility
		// remove later ..
		if err := r.Status().Update(r.DutyContext, obj); err != nil {
			logger.Errorf("[kopper] failed to update status %s: %v", resourceName, err)
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
	}

	action := lo.Ternary(isCreated, "Created", "Updated")
	logger.V(2).Infof("[kopper] %s %s", action, resourceName)
	r.Events.Event(obj, "Normal", action, fmt.Sprintf("%s %s", action, resourceName))
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
//
// Resources are watched as Unstructured to ensure cache synchronization succeeds
// even when some resources have specs that don't match the Go type definitions.
// Malformed resources are detected during the Unstructured-to-typed conversion
// in Reconcile(), where they can be handled gracefully with a warning event
// rather than crashing the controller.
func (r *Reconciler[T, PT]) SetupWithManager(mgr ctrl.Manager) error {
	pObj := PT(new(T))

	gvk, err := apiutil.GVKForObject(pObj, mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("failed to get GVK for object: %w", err)
	}
	r.gvk = gvk

	raw := &unstructured.Unstructured{}
	raw.SetGroupVersionKind(gvk)

	return ctrl.NewControllerManagedBy(mgr).
		For(raw).
		Complete(r)
}

// fromUnstructured converts an unstructured object to a typed object,
// recovering from any panics that may occur during conversion.
func fromUnstructured(u map[string]any, obj any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic converting unstructured object: %v", r)
		}
	}()
	return runtime.DefaultUnstructuredConverter.FromUnstructured(u, obj)
}

func isUniqueConstraintError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgerrcode.UniqueViolation
	}

	return false
}
