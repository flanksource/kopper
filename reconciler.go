package kopper

import (
	gocontext "context"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/context"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func SetupReconciler[T any, PT interface {
	*T
	client.Object
}](ctx context.Context, mgr ctrl.Manager, OnUpsertFunc func(context.Context, PT) error, OnDeleteFunc func(context.Context, string) error, finalizer string) (Reconciler[T, PT], error) {
	if finalizer == "" {
		return Reconciler[T, PT]{}, fmt.Errorf("field Finalizer cannot be empty")
	}

	r := Reconciler[T, PT]{
		DutyContext:  ctx,
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		OnUpsertFunc: OnUpsertFunc,
		OnDeleteFunc: OnDeleteFunc,
		Finalizer:    finalizer,
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
	DutyContext  context.Context
	Scheme       *runtime.Scheme
	OnUpsertFunc func(context.Context, PT) error
	OnDeleteFunc func(context.Context, string) error
	Finalizer    string
}

func (r *Reconciler[T, PT]) Reconcile(ctx gocontext.Context, req ctrl.Request) (ctrl.Result, error) {
	obj := PT(new(T))

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	resourceName := fmt.Sprintf("%s[%s]", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetUID())

	if !obj.GetDeletionTimestamp().IsZero() {
		logger.Infof("[kopper] deleting %s", resourceName)
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
		logger.Errorf("[kopper] failed to upsert %s: %v", resourceName, err)
		return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
	}

	if err := r.Status().Update(r.DutyContext, obj); err != nil {
		logger.Errorf("[kopper] failed to update status %s: %v", resourceName, err)
		return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
	}

	logger.Infof("[kopper] upserted %s", resourceName)
	return ctrl.Result{}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler[T, PT]) SetupWithManager(mgr ctrl.Manager) error {
	pObj := PT(new(T))
	return ctrl.NewControllerManagedBy(mgr).
		For(pObj).
		Complete(r)
}
