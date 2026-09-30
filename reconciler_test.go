package kopper

import (
	gocontext "context"
	"errors"
	"strings"
	"testing"

	"github.com/flanksource/duty/context"
	k8smeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

var widgetGV = schema.GroupVersion{Group: "test.kopper.io", Version: "v1"}

type widget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            widgetStatus `json:"status,omitempty"`
}

type widgetStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

func (w *widget) DeepCopyObject() runtime.Object {
	out := *w
	w.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Status.Conditions = append([]metav1.Condition(nil), w.Status.Conditions...)
	return &out
}

func (w *widget) GetStatusConditions() *[]metav1.Condition { return &w.Status.Conditions }
func (w *widget) SetObservedGeneration(g int64)            { w.Status.ObservedGeneration = g }
func (w *widget) GetObservedGeneration() int64             { return w.Status.ObservedGeneration }

type widgetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []widget `json:"items"`
}

func (l *widgetList) DeepCopyObject() runtime.Object {
	out := *l
	out.Items = make([]widget, len(l.Items))
	for i := range l.Items {
		out.Items[i] = *l.Items[i].DeepCopyObject().(*widget)
	}
	return &out
}

func newWidgetReconciler(t *testing.T, onUpsert OnUpsertFunc[*widget]) (*Reconciler[widget, *widget], client.Client, *events.FakeRecorder) {
	t.Helper()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(widgetGV, &widget{}, &widgetList{})
	metav1.AddToGroupVersion(scheme, widgetGV)

	obj := &widget{ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "default", Generation: 1, UID: "uid-1"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	recorder := events.NewFakeRecorder(10)

	return &Reconciler[widget, *widget]{
		Client:       c,
		DutyContext:  context.NewContext(gocontext.Background()),
		Scheme:       scheme,
		OnUpsertFunc: onUpsert,
		OnDeleteFunc: func(context.Context, string) error { return nil },
		Finalizer:    "test.kopper.io",
		Events:       recorder,
		gvk:          widgetGV.WithKind("widget"),
	}, c, recorder
}

func readyCondition(t *testing.T, c client.Client) *metav1.Condition {
	t.Helper()
	obj := &widget{}
	if err := c.Get(gocontext.Background(), types.NamespacedName{Namespace: "default", Name: "w"}, obj); err != nil {
		t.Fatalf("failed to get widget: %v", err)
	}
	return k8smeta.FindStatusCondition(obj.Status.Conditions, ReadyConditionType)
}

func drain(recorder *events.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-recorder.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestReconcileConditionError(t *testing.T) {
	valid := false
	r, c, recorder := newWidgetReconciler(t, func(context.Context, *widget) error {
		if !valid {
			return NewConditionError("RowLevelSecurityRequired", errors.New("row-level security is disabled"))
		}
		return nil
	})
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "w"}}

	result, err := r.Reconcile(gocontext.Background(), req)
	if err != nil || !result.IsZero() {
		t.Fatalf("expected no error and no requeue, got result=%+v err=%v", result, err)
	}

	cond := readyCondition(t, c)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "RowLevelSecurityRequired" ||
		cond.Message != "row-level security is disabled" {
		t.Fatalf("unexpected Ready condition: %+v", cond)
	}

	evts := drain(recorder)
	if len(evts) != 1 || !strings.Contains(evts[0], "Warning RowLevelSecurityRequired") {
		t.Fatalf("expected one warning event, got %v", evts)
	}

	// Reconciling again with the same outcome doesn't repeat the event
	if _, err := r.Reconcile(gocontext.Background(), req); err != nil {
		t.Fatal(err)
	}
	if evts := drain(recorder); len(evts) != 0 {
		t.Fatalf("expected no new events, got %v", evts)
	}

	// Once the external state changes, a reconcile makes it ready
	valid = true
	if _, err := r.Reconcile(gocontext.Background(), req); err != nil {
		t.Fatal(err)
	}
	if cond := readyCondition(t, c); cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != ReasonSynced {
		t.Fatalf("expected Ready=True, got %+v", cond)
	}
}

func TestReconcilePersistFailedIsRetried(t *testing.T) {
	r, c, _ := newWidgetReconciler(t, func(context.Context, *widget) error {
		return errors.New("db down")
	})
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "w"}}

	result, err := r.Reconcile(gocontext.Background(), req)
	if err == nil || result.RequeueAfter == 0 {
		t.Fatalf("expected an error and a requeue, got result=%+v err=%v", result, err)
	}
	if cond := readyCondition(t, c); cond == nil || cond.Reason != ReasonPersistFailed {
		t.Fatalf("expected PersistFailed, got %+v", cond)
	}
}

func TestConditionError(t *testing.T) {
	inner := errors.New("scope not found")
	err := NewConditionError("", inner)

	ce, ok := AsConditionError(errors.Join(errors.New("wrapped"), err))
	if !ok || ce.Reason != ReasonInvalid || !errors.Is(err, inner) || err.Error() != "scope not found" {
		t.Fatalf("unexpected condition error: %+v", ce)
	}

	if ce, ok := AsConditionError(ConditionErrorf("ScopeInvalid", "scope %s is invalid", "a")); !ok ||
		ce.Reason != "ScopeInvalid" || ce.Error() != "scope a is invalid" {
		t.Fatalf("unexpected condition error: %+v", ce)
	}
}

func TestResync(t *testing.T) {
	r, _, _ := newWidgetReconciler(t, func(context.Context, *widget) error { return nil })

	if err := r.Resync(gocontext.Background()); err == nil {
		t.Fatal("expected an error before the reconciler is set up")
	}

	r.resync = make(chan event.GenericEvent, 10)
	if err := r.Resync(gocontext.Background()); err != nil {
		t.Fatal(err)
	}

	select {
	case e := <-r.resync:
		if e.Object.GetName() != "w" || e.Object.GetNamespace() != "default" {
			t.Fatalf("unexpected object: %s/%s", e.Object.GetNamespace(), e.Object.GetName())
		}
	default:
		t.Fatal("expected the widget to be enqueued")
	}
}
