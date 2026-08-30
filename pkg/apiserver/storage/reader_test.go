package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

var (
	testGVR  = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	testKind = schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
)

func newTestStorage(t *testing.T, objectsByCluster map[string][]runtime.Object) *Storage {
	t.Helper()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(testKind.GroupVersion().WithKind("DeploymentList"), &unstructured.UnstructuredList{})

	clusters := make(map[string]dynamic.Interface, len(objectsByCluster))
	for cluster, objects := range objectsByCluster {
		clusters[cluster] = dynamicfake.NewSimpleDynamicClient(scheme, objects...)
	}

	storage, err := New(Options{
		GVR:        testGVR,
		Kind:       testKind,
		Namespaced: true,
		Singular:   "deployment",
		Clusters:   clusters,
	})
	require.NoError(t, err)
	return storage
}

func deployment(namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{}}
	obj.SetGroupVersionKind(testKind)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func testContext(namespace string) context.Context {
	return request.WithNamespace(context.Background(), namespace)
}

func TestStorage_Get(t *testing.T) {
	t.Parallel()

	t.Run("single hit returns stamped object", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "web")},
			"west": {},
		})

		obj, err := storage.Get(testContext("default"), "web", &metav1.GetOptions{})
		require.NoError(t, err)

		got := obj.(*unstructured.Unstructured)
		assert.Equal(t, "web", got.GetName())
		assert.Equal(t, "east", got.GetAnnotations()[v1alpha1.ClusterAnnotation])
		assert.Equal(t, "east", got.GetLabels()[v1alpha1.ClusterLabel])
	})

	t.Run("no hit is NotFound", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {},
			"west": {},
		})

		_, err := storage.Get(testContext("default"), "missing", &metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "expected NotFound, got %v", err)
	})

	t.Run("ambiguous hit is a conflict naming candidate clusters", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "web")},
			"west": {deployment("default", "web")},
		})

		_, err := storage.Get(testContext("default"), "web", &metav1.GetOptions{})
		require.True(t, apierrors.IsConflict(err), "expected Conflict, got %v", err)
		assert.Contains(t, err.Error(), "east, west")
	})
}

func TestStorage_List(t *testing.T) {
	t.Parallel()

	storage := newTestStorage(t, map[string][]runtime.Object{
		"east": {deployment("default", "web"), deployment("default", "api")},
		"west": {deployment("default", "web")},
	})

	t.Run("fan-out merges and stamps all clusters", func(t *testing.T) {
		t.Parallel()

		obj, err := storage.List(testContext("default"), &metainternalversion.ListOptions{})
		require.NoError(t, err)

		list := obj.(*unstructured.UnstructuredList)
		require.Len(t, list.Items, 3)
		// Sorted by namespace, name, cluster.
		assert.Equal(t, "api", list.Items[0].GetName())
		assert.Equal(t, "east", list.Items[1].GetAnnotations()[v1alpha1.ClusterAnnotation])
		assert.Equal(t, "west", list.Items[2].GetAnnotations()[v1alpha1.ClusterAnnotation])
		assert.Empty(t, list.GetResourceVersion(), "cross-cluster list resourceVersion is meaningless and must stay empty")
	})

	t.Run("virtual cluster label narrows the fan-out", func(t *testing.T) {
		t.Parallel()

		selector, err := labels.Parse(v1alpha1.ClusterLabel + "=west")
		require.NoError(t, err)

		obj, err := storage.List(testContext("default"), &metainternalversion.ListOptions{LabelSelector: selector})
		require.NoError(t, err)

		list := obj.(*unstructured.UnstructuredList)
		require.Len(t, list.Items, 1)
		assert.Equal(t, "west", list.Items[0].GetAnnotations()[v1alpha1.ClusterAnnotation])
	})
}
