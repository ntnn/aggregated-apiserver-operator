package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/rest"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

func TestStorage_Create(t *testing.T) {
	t.Parallel()

	t.Run("routes via cluster annotation and strips aggregator metadata", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {},
			"west": {},
		})

		obj := deployment("default", "web")
		obj.SetAnnotations(map[string]string{v1alpha1.ClusterAnnotation: "west"})
		obj.SetLabels(map[string]string{v1alpha1.ClusterLabel: "west", "app": "web"})

		created, err := storage.Create(testContext("default"), obj, nil, &metav1.CreateOptions{})
		require.NoError(t, err)

		got := created.(*unstructured.Unstructured)
		assert.Equal(t, "west", got.GetAnnotations()[v1alpha1.ClusterAnnotation], "response must be stamped")

		remote, err := storage.opts.Clusters["west"].
			Resource(testGVR).
			Namespace("default").
			Get(t.Context(), "web", metav1.GetOptions{})
		require.NoError(t, err)
		assert.NotContains(t, remote.GetAnnotations(), v1alpha1.ClusterAnnotation, "aggregator annotation must not land on the member")
		assert.NotContains(t, remote.GetLabels(), v1alpha1.ClusterLabel, "virtual label must not land on the member")
		assert.Equal(t, "web", remote.GetLabels()["app"], "user labels must survive")

		_, err = storage.opts.Clusters["east"].
			Resource(testGVR).
			Namespace("default").
			Get(t.Context(), "web", metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "create must land on the target cluster only")
	})

	t.Run("missing annotation is a bad request", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{"east": {}})

		_, err := storage.Create(testContext("default"), deployment("default", "web"), nil, &metav1.CreateOptions{})
		assert.True(t, apierrors.IsBadRequest(err), "expected BadRequest, got %v", err)
	})

	t.Run("unknown cluster is a bad request", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{"east": {}})

		obj := deployment("default", "web")
		obj.SetAnnotations(map[string]string{v1alpha1.ClusterAnnotation: "nowhere"})

		_, err := storage.Create(testContext("default"), obj, nil, &metav1.CreateOptions{})
		assert.True(t, apierrors.IsBadRequest(err), "expected BadRequest, got %v", err)
	})
}

func TestStorage_Update(t *testing.T) {
	t.Parallel()

	t.Run("routes to the owning cluster", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {},
			"west": {deployment("default", "web")},
		})

		updated := deployment("default", "web")
		updated.SetLabels(map[string]string{"app": "web"})

		obj, _, err := storage.Update(testContext("default"), "web",
			rest.DefaultUpdatedObjectInfo(updated), nil, nil, false, &metav1.UpdateOptions{})
		require.NoError(t, err)

		got := obj.(*unstructured.Unstructured)
		assert.Equal(t, "west", got.GetAnnotations()[v1alpha1.ClusterAnnotation])

		remote, err := storage.opts.Clusters["west"].
			Resource(testGVR).
			Namespace("default").
			Get(t.Context(), "web", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "web", remote.GetLabels()["app"])
		assert.NotContains(t, remote.GetAnnotations(), v1alpha1.ClusterAnnotation)
	})

	t.Run("ambiguous name conflicts", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "web")},
			"west": {deployment("default", "web")},
		})

		_, _, err := storage.Update(testContext("default"), "web",
			rest.DefaultUpdatedObjectInfo(deployment("default", "web")), nil, nil, false, &metav1.UpdateOptions{})
		assert.True(t, apierrors.IsConflict(err), "expected Conflict, got %v", err)
	})

	t.Run("missing object is NotFound", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{"east": {}})

		_, _, err := storage.Update(testContext("default"), "missing",
			rest.DefaultUpdatedObjectInfo(deployment("default", "missing")), nil, nil, false, &metav1.UpdateOptions{})
		assert.True(t, apierrors.IsNotFound(err), "expected NotFound, got %v", err)
	})
}

func TestStorage_Delete(t *testing.T) {
	t.Parallel()

	t.Run("routes to the owning cluster", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "web")},
			"west": {},
		})

		_, _, err := storage.Delete(testContext("default"), "web", nil, &metav1.DeleteOptions{})
		require.NoError(t, err)

		_, err = storage.opts.Clusters["east"].
			Resource(testGVR).
			Namespace("default").
			Get(t.Context(), "web", metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "object must be gone from the owning cluster")
	})

	t.Run("ambiguous name conflicts", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "web")},
			"west": {deployment("default", "web")},
		})

		_, _, err := storage.Delete(testContext("default"), "web", nil, &metav1.DeleteOptions{})
		assert.True(t, apierrors.IsConflict(err), "expected Conflict, got %v", err)
	})
}

func TestStorage_DeleteCollection(t *testing.T) {
	t.Parallel()

	t.Run("fans out over all clusters", func(t *testing.T) {
		t.Parallel()

		storage := newTestStorage(t, map[string][]runtime.Object{
			"east": {deployment("default", "a")},
			"west": {deployment("default", "b")},
		})

		obj, err := storage.DeleteCollection(testContext("default"), nil, &metav1.DeleteOptions{}, &metainternalversion.ListOptions{})
		require.NoError(t, err)

		list := obj.(*unstructured.UnstructuredList)
		assert.Len(t, list.Items, 2)
		clusters := map[string]bool{}
		for _, item := range list.Items {
			clusters[item.GetAnnotations()[v1alpha1.ClusterAnnotation]] = true
		}
		// fake dynamic client no-ops DeleteCollection, so member emptiness is
		// asserted in the integration test; here only the fan-out and stamping
		assert.True(t, clusters["east"] && clusters["west"], "delete must fan out over both clusters")
	})
}
