package kopper

import (
	gocontext "context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const resyncBufferSize = 1024

// Enqueue triggers a reconcile of a single resource, e.g. when something it
// depends on outside of Kubernetes has changed.
// It blocks until the request is queued or ctx is done.
func (r *Reconciler[T, PT]) Enqueue(ctx gocontext.Context, namespace, name string) error {
	if r.resync == nil {
		return fmt.Errorf("reconciler is not set up with a manager")
	}

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(r.gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)

	select {
	case r.resync <- event.GenericEvent{Object: obj}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Resync triggers a reconcile of every resource of this kind, so resources
// whose validity depends on state outside of Kubernetes are re-evaluated
// without being re-applied. opts can narrow the resources, e.g. client.InNamespace.
func (r *Reconciler[T, PT]) Resync(ctx gocontext.Context, opts ...client.ListOption) error {
	if r.resync == nil {
		return fmt.Errorf("reconciler is not set up with a manager")
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(r.gvk.GroupVersion().WithKind(r.gvk.Kind + "List"))
	if err := r.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("failed to list %s: %w", r.gvk.Kind, err)
	}

	for _, item := range list.Items {
		if err := r.Enqueue(ctx, item.GetNamespace(), item.GetName()); err != nil {
			return err
		}
	}

	return nil
}
