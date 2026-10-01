package tests

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dutyctx "github.com/flanksource/duty/context"
	"github.com/flanksource/kopper"
	v1 "github.com/flanksource/kopper/tests/api/v1"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	corev1 "k8s.io/api/core/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestReadinessAndEnqueue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	ext, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	crd, err := loadCRD("crds/test.kopper.io_testresources_v2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	crd.Spec.Group = "readiness.kopper.io"
	crd.Spec.Names.Kind = "ReadinessResource"
	crd.Spec.Names.ListKind = "ReadinessResourceList"
	crd.Spec.Names.Plural = "readinessresources"
	crd.Spec.Names.Singular = "readinessresource"
	crd.Name = crd.Spec.Names.Plural + "." + crd.Spec.Group
	if err := applyCRD(ctx, ext, crd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ext.ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crd.Name, metav1.DeleteOptions{})
	})
	if err := waitForCRD(ctx, ext, crd.Name); err != nil {
		t.Fatal(err)
	}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	gv := schema.GroupVersion{Group: crd.Spec.Group, Version: "v1"}
	scheme.AddKnownTypeWithName(gv.WithKind(crd.Spec.Names.Kind), &v1.TestResource{})
	scheme.AddKnownTypeWithName(gv.WithKind(crd.Spec.Names.ListKind), &v1.TestResourceList{})
	metav1.AddToGroupVersion(scheme, gv)
	kube, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "readiness-" + rand.String(5)}}
	otherNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns.Name + "-other"}}
	for _, namespace := range []*corev1.Namespace{ns, otherNS} {
		if err := kube.Create(ctx, namespace); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = kube.Delete(context.Background(), namespace) })
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		resources := &v1.TestResourceList{}
		if err := kube.List(cleanupCtx, resources); err != nil {
			t.Error(err)
			return
		}
		for _, resource := range resources.Items {
			if resource.Namespace != ns.Name && resource.Namespace != otherNS.Name {
				continue
			}
			resource.Finalizers = nil
			if err := kube.Update(cleanupCtx, &resource); err != nil {
				t.Error(err)
			}
			if err := kube.Delete(cleanupCtx, &resource); err != nil {
				t.Error(err)
			}
		}
	})
	obj := &v1.TestResource{ObjectMeta: metav1.ObjectMeta{Name: "operators", Namespace: ns.Name}}
	if err := kube.Create(ctx, obj); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(obj)
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Client:  client.Options{Cache: &client.CacheOptions{Unstructured: true}},
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var ready, block atomic.Bool
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	r, err := kopper.SetupReconciler(dutyctx.NewContext(ctx), mgr,
		func(_ dutyctx.Context, obj *v1.TestResource) error {
			if block.Load() && obj.Name == key.Name {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
			}
			if !ready.Load() {
				return kopper.NotReady("ScopeNotFound", "Scope %s is missing", "staging")
			}
			return nil
		}, func(dutyctx.Context, string) error { return nil }, nil, "readiness.kopper.io")
	if err != nil {
		t.Fatal(err)
	}

	// Use the API directly before manager startup to check exact reconcile results.
	// The registered reconciler retains its cached client and original callbacks.
	r.Client = kube
	recorder := events.NewFakeRecorder(20)
	r.Events = recorder
	conflicts := 0
	r.OnConflictFunc = func(dutyctx.Context, *v1.TestResource) error { conflicts++; return nil }
	plainErr := errors.New("storage unavailable")
	cause := &pgconn.PgError{Code: pgerrcode.UniqueViolation, Message: "scope name is already used"}
	uniqueErr := fmt.Errorf("save: %w", cause)
	underlyingErr := &kopper.NotReadyError{Err: uniqueErr}
	formattedErr := kopper.NotReady("", "invalid role: %w", uniqueErr)
	overriddenErr := &kopper.NotReadyError{Message: "choose another scope", Err: uniqueErr}
	for _, err := range []error{underlyingErr, formattedErr, overriddenErr} {
		var pgErr *pgconn.PgError
		if !errors.Is(err, cause) || !errors.As(err, &pgErr) || pgErr != cause {
			t.Fatalf("underlying error was lost: %v", err)
		}
	}
	for _, tc := range []struct {
		name, reason, message string
		upsertErr             error
		retry                 time.Duration
		wantErr, warning      bool
	}{
		{"wrapped", "ScopeNotFound", "missing scope", fmt.Errorf("persist: %w", &kopper.NotReadyError{Reason: "ScopeNotFound", Message: "missing scope", RequeueAfter: 17 * time.Second}), 17 * time.Second, false, true},
		{"same condition", "ScopeNotFound", "missing scope", kopper.NotReady("ScopeNotFound", "missing %s", "scope"), 0, false, false},
		{"message only", "ScopeNotFound", "still missing", kopper.NotReady("ScopeNotFound", "still missing"), 0, false, false},
		{"reason changed", "AgentNotFound", "missing agent", kopper.NotReady("AgentNotFound", "missing agent"), 0, false, true},
		{"ready again", kopper.ReasonSynced, "", nil, 0, false, false},
		{"invalid again", "AgentNotFound", "missing agent", kopper.NotReady("AgentNotFound", "missing agent"), 0, false, true},
		{"underlying error and empty reason", "Invalid", uniqueErr.Error(), underlyingErr, 0, false, true},
		{"formatted cause and empty reason", "Invalid", "invalid role: " + uniqueErr.Error(), formattedErr, 0, false, false},
		{"message overrides cause", "Invalid", "choose another scope", overriddenErr, 0, false, false},
		{"plain error", kopper.ReasonPersistFailed, plainErr.Error(), plainErr, 2 * time.Minute, true, false},
		{"unique conflict", kopper.ReasonPersistFailed, plainErr.Error(), uniqueErr, 15 * time.Second, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.OnUpsertFunc = func(dutyctx.Context, *v1.TestResource) error { return tc.upsertErr }
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			if (err != nil) != tc.wantErr || (tc.wantErr && !errors.Is(err, tc.upsertErr)) || result.RequeueAfter != tc.retry || result.Requeue != tc.wantErr {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if err := kube.Get(ctx, key, obj); err != nil {
				t.Fatal(err)
			}
			condition := apiMeta.FindStatusCondition(obj.Status.Conditions, kopper.ReadyConditionType)
			status := metav1.ConditionFalse
			if tc.upsertErr == nil {
				status = metav1.ConditionTrue
			}
			if condition == nil || condition.Status != status || condition.Reason != tc.reason || condition.Message != tc.message || condition.ObservedGeneration != obj.Generation || obj.Status.ObservedGeneration != obj.Generation {
				t.Fatalf("unexpected status: %+v, generation=%d", obj.Status, obj.Generation)
			}
			select {
			case event := <-recorder.Events:
				if !tc.warning || !strings.HasPrefix(event, "Warning "+tc.reason+" ") {
					t.Fatalf("unexpected event: %s", event)
				}
			default:
				if tc.warning {
					t.Fatal("missing warning event")
				}
			}
		})
	}
	if conflicts != 1 {
		t.Fatalf("conflict callback count=%d", conflicts)
	}

	var unconfigured kopper.Reconciler[v1.TestResource, *v1.TestResource]
	if err := unconfigured.Resync(ctx); err == nil {
		t.Fatal("expected Resync to fail before setup")
	}
	canceledCtx, cancelResync := context.WithCancel(ctx)
	cancelResync()
	if err := r.Resync(canceledCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled Resync, got %v", err)
	}
	if err := r.Resync(ctx, client.InNamespace(ns.Name)); err != nil {
		t.Fatalf("Resync before manager startup: %v", err)
	}
	r.Enqueue(ns.Name, "does-not-exist")
	mgrCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- mgr.Start(mgrCtx) }()
	defer func() {
		unblock.Do(func() { close(release) })
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	awaitCondition := func(key client.ObjectKey, status metav1.ConditionStatus, reason string) {
		t.Helper()
		if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 15*time.Second, true, func(ctx context.Context) (bool, error) {
			current := &v1.TestResource{}
			if err := kube.Get(ctx, key, current); err != nil {
				return false, err
			}
			condition := apiMeta.FindStatusCondition(current.Status.Conditions, kopper.ReadyConditionType)
			return condition != nil && condition.Status == status && condition.Reason == reason, nil
		}); err != nil {
			t.Fatalf("waiting for %s %s: %v", key, reason, err)
		}
	}
	awaitCondition(key, metav1.ConditionFalse, "ScopeNotFound")
	cachedClient, err := client.NewWithWatch(cfg, client.Options{
		Scheme: scheme,
		Cache:  &client.CacheOptions{Reader: mgr.GetCache(), Unstructured: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	cached := r
	cached.Client = cachedClient
	emptySelection := client.MatchingLabels{"group": "no-match"}
	if err := cached.Resync(ctx, emptySelection); err != nil {
		t.Fatalf("empty cached Resync: %v", err)
	}
	t.Run("canceled empty cached resync", func(t *testing.T) {
		if err := cached.Resync(canceledCtx, emptySelection); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
	t.Run("canceled during empty cached list", func(t *testing.T) {
		listCtx, cancelList := context.WithCancel(ctx)
		defer cancelList()
		cached.Client = interceptor.NewClient(cachedClient, interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				err := c.List(ctx, list, opts...)
				cancelList()
				return err
			},
		})
		if err := cached.Resync(listCtx, emptySelection); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled after listing, got %v", err)
		}
	})
	other := &v1.TestResource{ObjectMeta: metav1.ObjectMeta{Name: "waiting", Namespace: ns.Name, Labels: map[string]string{"group": "selected"}}}
	if err := kube.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherKey := client.ObjectKeyFromObject(other)
	awaitCondition(otherKey, metav1.ConditionFalse, "ScopeNotFound")
	time.Sleep(200 * time.Millisecond)
	block.Store(true)
	r.Enqueue(ns.Name, key.Name)
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("enqueue did not trigger reconciliation")
	}

	// Fill beyond a typical channel buffer while the only worker is blocked.
	var senders sync.WaitGroup
	for worker := range 8 {
		senders.Add(1)
		go func() {
			defer senders.Done()
			for i := range 256 {
				r.Enqueue(ns.Name, fmt.Sprintf("missing-%d-%d", worker, i))
			}
		}()
	}
	enqueued := make(chan struct{})
	go func() {
		senders.Wait()
		r.Enqueue(ns.Name, other.Name)
		close(enqueued)
	}()
	select {
	case <-enqueued:
	case <-time.After(5 * time.Second):
		t.Fatal("Enqueue blocked behind a busy worker")
	}
	ready.Store(true)
	unblock.Do(func() { close(release) })
	awaitCondition(otherKey, metav1.ConditionTrue, kopper.ReasonSynced)
	if err := kube.Get(ctx, otherKey, other); err != nil {
		t.Fatal(err)
	}
	if other.Generation != 1 || other.Status.ObservedGeneration != 1 {
		t.Fatalf("enqueue changed generation: %+v", other)
	}
	t.Log("Ready recovered without a spec change after concurrent enqueue under load")

	outside := &v1.TestResource{ObjectMeta: metav1.ObjectMeta{Name: "outside", Namespace: otherNS.Name, Labels: map[string]string{"group": "selected"}}}
	if err := kube.Create(ctx, outside); err != nil {
		t.Fatal(err)
	}
	outsideKey := client.ObjectKeyFromObject(outside)
	awaitCondition(outsideKey, metav1.ConditionTrue, kopper.ReasonSynced)
	awaitCondition(key, metav1.ConditionTrue, kopper.ReasonSynced)
	time.Sleep(200 * time.Millisecond)
	ready.Store(false)
	if err := r.Resync(ctx, client.InNamespace(ns.Name), client.MatchingLabels{"group": "selected"}); err != nil {
		t.Fatal(err)
	}
	awaitCondition(otherKey, metav1.ConditionFalse, "ScopeNotFound")
	for _, excluded := range []client.ObjectKey{key, outsideKey} {
		current := &v1.TestResource{}
		if err := kube.Get(ctx, excluded, current); err != nil {
			t.Fatal(err)
		}
		condition := apiMeta.FindStatusCondition(current.Status.Conditions, kopper.ReadyConditionType)
		if condition == nil || condition.Status != metav1.ConditionTrue {
			t.Fatalf("filtered Resync reconciled excluded resource %s: %+v", excluded, condition)
		}
	}
	if err := r.Resync(ctx); err != nil {
		t.Fatal(err)
	}
	awaitCondition(key, metav1.ConditionFalse, "ScopeNotFound")
	awaitCondition(outsideKey, metav1.ConditionFalse, "ScopeNotFound")
	t.Log("Resync respected namespace and label filters, then reconciled all resources")
}
