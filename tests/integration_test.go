package tests

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dutyctx "github.com/flanksource/duty/context"
	"github.com/flanksource/kopper"
	v1 "github.com/flanksource/kopper/tests/api/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func TestSchemaChangeDoesNotCrashController(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := ctrl.GetConfig()
	if err != nil {
		t.Fatalf("failed to load kubeconfig: %v", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create kubernetes client: %v", err)
	}

	apiExtClient, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create api extensions client: %v", err)
	}

	crdV1Path := filepath.Join("crds", "test.kopper.io_testresources_v1.yaml")
	crdV2Path := filepath.Join("crds", "test.kopper.io_testresources_v2.yaml")

	crdV1, err := loadCRD(crdV1Path)
	if err != nil {
		t.Fatalf("failed to load v1 crd: %v", err)
	}

	crdV2, err := loadCRD(crdV2Path)
	if err != nil {
		t.Fatalf("failed to load v2 crd: %v", err)
	}

	if err := applyCRD(ctx, apiExtClient, crdV1); err != nil {
		t.Fatalf("failed to apply v1 crd: %v", err)
	}

	if err := waitForCRD(ctx, apiExtClient, crdV1.Name); err != nil {
		t.Fatalf("failed waiting for v1 crd: %v", err)
	}

	t.Cleanup(func() {
		_ = apiExtClient.ApiextensionsV1().CustomResourceDefinitions().Delete(context.Background(), crdV1.Name, metav1.DeleteOptions{})
	})

	namespace := fmt.Sprintf("kopper-e2e-%s", rand.String(5))
	_, err = kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create namespace: %v", err)
	}

	t.Cleanup(func() {
		_ = kubeClient.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{})
	})

	legacyHeaders := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "test.kopper.io/v1",
			"kind":       "TestResource",
			"metadata": map[string]interface{}{
				"name":      "legacy-one",
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"message": "legacy headers",
				"headers": map[string]interface{}{},
			},
		},
	}

	dynClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create dynamic client: %v", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    "test.kopper.io",
		Version:  "v1",
		Resource: "testresources",
	}

	_, err = dynClient.Resource(gvr).Namespace(namespace).Create(ctx, legacyHeaders, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create legacy resource: %v", err)
	}

	if err := applyCRD(ctx, apiExtClient, crdV2); err != nil {
		t.Fatalf("failed to apply v2 crd: %v", err)
	}

	if err := waitForCRD(ctx, apiExtClient, crdV2.Name); err != nil {
		t.Fatalf("failed waiting for v2 crd: %v", err)
	}

	if err := v1.AddToScheme(scheme.Scheme); err != nil {
		t.Fatalf("failed to add test scheme: %v", err)
	}

	var mu sync.Mutex
	upserted := make(map[string]bool)

	onUpsert := func(ctx dutyctx.Context, obj *v1.TestResource) error {
		mu.Lock()
		defer mu.Unlock()
		t.Logf("onUpsert called for: %s", obj.Name)
		upserted[obj.Name] = true
		return nil
	}

	onDelete := func(ctx dutyctx.Context, id string) error {
		return nil
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	dutyCtx := dutyctx.NewContext(ctx)
	_, err = kopper.SetupReconciler(dutyCtx, mgr, onUpsert, onDelete, nil, "test.kopper.io")
	if err != nil {
		t.Fatalf("failed to setup reconciler: %v", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()

	mgrDone := make(chan error, 1)
	go func() {
		mgrDone <- mgr.Start(mgrCtx)
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache failed to sync")
	}
	t.Log("Cache synced successfully")

	checkUpserted := func(name string, expected bool) bool {
		mu.Lock()
		defer mu.Unlock()
		return upserted[name] == expected
	}

	time.Sleep(2 * time.Second)

	select {
	case err := <-mgrDone:
		t.Fatalf("manager stopped unexpectedly: %v", err)
	default:
		t.Log("✓ Controller still running after schema change")
	}

	if !checkUpserted("legacy-one", false) {
		t.Error("expected legacy resource to be skipped after schema change")
	} else {
		t.Log("✓ Legacy resource was skipped")
	}

	t.Log("Creating valid resource 'good-one' with array headers")
	validResource := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "test.kopper.io/v1",
			"kind":       "TestResource",
			"metadata": map[string]interface{}{
				"name":      "good-one",
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"message": "hello",
				"headers": []interface{}{
					map[string]interface{}{"name": "x-test", "value": "value1"},
				},
			},
		},
	}
	_, err = dynClient.Resource(gvr).Namespace(namespace).Create(ctx, validResource, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create valid resource: %v", err)
	}

	reconciled := false
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		if checkUpserted("good-one", true) {
			reconciled = true
			break
		}
	}
	if !reconciled {
		t.Error("expected 'good-one' to be reconciled")
	} else {
		t.Log("✓ Valid resource 'good-one' was reconciled")
	}
}

func loadCRD(path string) (*apiextensionsv1.CustomResourceDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 1024)
	if err := decoder.Decode(crd); err != nil {
		return nil, err
	}

	return crd, nil
}

func applyCRD(ctx context.Context, client apiextensionsclient.Interface, crd *apiextensionsv1.CustomResourceDefinition) error {
	_, err := client.ApiextensionsV1().CustomResourceDefinitions().Create(ctx, crd, metav1.CreateOptions{})
	if err == nil {
		return nil
	}

	if !apierrors.IsAlreadyExists(err) {
		return err
	}

	existing, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, crd.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	crd.ResourceVersion = existing.ResourceVersion
	_, err = client.ApiextensionsV1().CustomResourceDefinitions().Update(ctx, crd, metav1.UpdateOptions{})
	return err
}

func waitForCRD(ctx context.Context, client apiextensionsclient.Interface, name string) error {
	return wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		for _, condition := range crd.Status.Conditions {
			if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
				return true, nil
			}
		}

		return false, nil
	})
}
