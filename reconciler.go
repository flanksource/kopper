package kopper

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func SetupReconciler[T any, PT interface {
	*T
	client.Object
}](mgr ctrl.Manager, OnUpsertFunc func(PT) error, OnDeleteFunc func(string) error, finalizer string) error {
	if finalizer == "" {
		return fmt.Errorf("field Finalizer cannot be empty")
	}

	r := Reconciler[T, PT]{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		OnUpsertFunc: OnUpsertFunc,
		OnDeleteFunc: OnDeleteFunc,
		Finalizer:    finalizer,
	}

	return r.SetupWithManager(mgr)
}

type Reconciler[T any, PT interface {
	*T
	client.Object
}] struct {
	client.Client
	Scheme       *runtime.Scheme
	OnUpsertFunc func(PT) error
	OnDeleteFunc func(string) error
	Finalizer    string
}

func (r *Reconciler[T, PT]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
		if err := r.OnDeleteFunc(string(obj.GetUID())); err != nil {
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

	if err := r.OnUpsertFunc(obj); err != nil {
		logger.Errorf("[kopper] failed to upsert %s: %v", resourceName, err)
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
