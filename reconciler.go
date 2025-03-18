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
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
}

func (r *Reconciler[T, PT]) Reconcile(ctx gocontext.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := PT(new(T))

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	original := obj.DeepCopyObject()

	resourceName := fmt.Sprintf("%s[%s]", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetUID())

	if !obj.GetDeletionTimestamp().IsZero() {
		logger.V(2).Infof("[kopper] deleting %s", resourceName)
		if err := r.OnDeleteFunc(r.DutyContext, string(obj.GetUID())); err != nil {
			logger.Errorf("[kopper] failed to delete %s: %v", resourceName, err)
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
		controllerutil.RemoveFinalizer(obj, r.Finalizer)
		return ctrl.Result{}, r.Update(ctx, obj)
	}

	if !controllerutil.ContainsFinalizer(obj, r.Finalizer) {
		controllerutil.AddFinalizer(obj, r.Finalizer)
		if err := r.Update(ctx, obj); err != nil {
			logger.Errorf("[kopper] failed to update finalizers %s: %v", resourceName, err)
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
	}

	if err := r.OnUpsertFunc(r.DutyContext, obj); err != nil {
		if isUniqueConstraintError(err) && r.OnConflictFunc != nil {
			logger.V(2).Infof("[kopper] deleting %s due to unique constraint violation", resourceName)

			if err := r.OnConflictFunc(r.DutyContext, obj); err != nil {
				logger.Errorf("[kopper] failed to delete %s: %v", resourceName, err)
				return ctrl.Result{}, err // retry immediately
			}

			return ctrl.Result{Requeue: true, RequeueAfter: time.Second * 5}, err // immediately retry
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

	logger.V(2).Infof("[kopper] upserted %s", resourceName)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler[T, PT]) SetupWithManager(mgr ctrl.Manager) error {
	pObj := PT(new(T))
	return ctrl.NewControllerManagedBy(mgr).
		For(pObj).
		Complete(r)
}

func isUniqueConstraintError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgerrcode.UniqueViolation
	}

	return false
}
