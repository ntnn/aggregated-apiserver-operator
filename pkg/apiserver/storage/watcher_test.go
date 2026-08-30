package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// nextUnstructured waits for one event and requires an unstructured object.
func nextUnstructured(t *testing.T, events <-chan watch.Event) (watch.EventType, *unstructured.Unstructured) {
	t.Helper()

	select {
	case event, ok := <-events:
		require.True(t, ok, "watch closed unexpectedly")
		obj, isUnstructured := event.Object.(*unstructured.Unstructured)
		require.True(t, isUnstructured, "unexpected object type %T", event.Object)
		return event.Type, obj
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for an event")
		panic("unreachable")
	}
}

func requireClosed(t *testing.T, events <-chan watch.Event) {
	t.Helper()

	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the watch to close")
		}
	}
}

func TestStorage_Watch(t *testing.T) {
	t.Parallel()

	storage := newTestStorage(t, map[string][]runtime.Object{
		"east": {},
		"west": {},
	})

	watcher, err := storage.Watch(testContext("default"), &metainternalversion.ListOptions{})
	require.NoError(t, err)
	t.Cleanup(watcher.Stop)

	_, err = storage.opts.Clusters["east"].
		Resource(testGVR).
		Namespace("default").
		Create(t.Context(), deployment("default", "web"), metav1.CreateOptions{})
	require.NoError(t, err)

	eventType, obj := nextUnstructured(t, watcher.ResultChan())

	assert.Equal(t, watch.Added, eventType)
	assert.Equal(t, "web", obj.GetName())
	assert.Equal(t, "east", obj.GetAnnotations()[v1alpha1.ClusterAnnotation])
	assert.Equal(t, "east", obj.GetLabels()[v1alpha1.ClusterLabel])
}

func TestStorage_Watch_clusterFiltered(t *testing.T) {
	t.Parallel()

	storage := newTestStorage(t, map[string][]runtime.Object{
		"east": {},
		"west": {},
	})

	selector, err := labels.Parse(v1alpha1.ClusterLabel + "=west")
	require.NoError(t, err)

	watcher, err := storage.Watch(testContext("default"), &metainternalversion.ListOptions{LabelSelector: selector})
	require.NoError(t, err)
	t.Cleanup(watcher.Stop)

	// created first, but must never arrive: wrong cluster
	_, err = storage.opts.Clusters["east"].
		Resource(testGVR).
		Namespace("default").
		Create(t.Context(), deployment("default", "east-only"), metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = storage.opts.Clusters["west"].
		Resource(testGVR).
		Namespace("default").
		Create(t.Context(), deployment("default", "west-only"), metav1.CreateOptions{})
	require.NoError(t, err)

	_, obj := nextUnstructured(t, watcher.ResultChan())
	assert.Equal(t, "west-only", obj.GetName(), "the east event must be filtered out")
}

func TestStorage_Watch_stop(t *testing.T) {
	t.Parallel()

	storage := newTestStorage(t, map[string][]runtime.Object{"east": {}})

	watcher, err := storage.Watch(testContext("default"), &metainternalversion.ListOptions{})
	require.NoError(t, err)

	watcher.Stop()
	requireClosed(t, watcher.ResultChan())
}

func TestStorage_Watch_done(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	storage := newTestStorage(t, map[string][]runtime.Object{"east": {}})
	storage.opts.Done = done

	watcher, err := storage.Watch(testContext("default"), &metainternalversion.ListOptions{})
	require.NoError(t, err)
	t.Cleanup(watcher.Stop)

	// closing Done must end running watches
	close(done)
	requireClosed(t, watcher.ResultChan())
}
