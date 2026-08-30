package integration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aggregationv1alpha1 "github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	"github.com/ntnn/aggregated-apiserver-operator/test/integration/framework"
)

// expectEvent waits for the next watch event and asserts type, name and source cluster.
func expectEvent(t *testing.T, events <-chan watch.Event, eventType watch.EventType, name, cluster string) {
	t.Helper()

	for {
		select {
		case event, ok := <-events:
			require.True(t, ok, "watch closed while waiting for %s %q", eventType, name)
			if event.Type == watch.Bookmark {
				continue
			}
			cm, isConfigMap := event.Object.(*corev1.ConfigMap)
			require.True(t, isConfigMap, "unexpected object type %T", event.Object)
			require.Equal(t, eventType, event.Type)
			require.Equal(t, name, cm.Name)
			require.Equal(t, cluster, cm.Annotations[aggregationv1alpha1.ClusterAnnotation], "source-cluster annotation")
			require.Equal(t, cluster, cm.Labels[aggregationv1alpha1.ClusterLabel], "virtual cluster label")
			return
		case <-time.After(30 * time.Second):
			t.Fatalf("timed out waiting for %s %q", eventType, name)
		}
	}
}

// expectClosed waits for the watch channel to close.
func expectClosed(t *testing.T, events <-chan watch.Event) {
	t.Helper()

	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for the watch to close")
		}
	}
}

func TestAggregatedWatch(t *testing.T) {
	t.Parallel()

	newAggregatedAPI := func() *aggregationv1alpha1.AggregatedAPI {
		return &aggregationv1alpha1.AggregatedAPI{
			ObjectMeta: metav1.ObjectMeta{Name: "watch"},
			Spec: aggregationv1alpha1.AggregatedAPISpec{
				Clusters: []aggregationv1alpha1.AggregatedCluster{
					{
						Access: aggregationv1alpha1.ClusterAccess{KubeconfigName: "member-a"},
						APIs:   []aggregationv1alpha1.APISelector{{Group: "", Resources: []string{"configmaps"}}},
					},
					{
						Access: aggregationv1alpha1.ClusterAccess{KubeconfigName: "member-b"},
						APIs:   []aggregationv1alpha1.APISelector{{Group: "", Resources: []string{"configmaps"}}},
					},
				},
			},
		}
	}

	seed := func(t *testing.T, cl client.Client, name string) *corev1.ConfigMap {
		t.Helper()
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "default",
			},
		}
		require.NoError(t, cl.Create(t.Context(), cm))
		return cm
	}

	t.Run("events from all clusters are stamped and multiplexed", func(t *testing.T) {
		t.Parallel()

		h := framework.New(t, []string{"member-a", "member-b"}, newAggregatedAPI())

		watcher, err := h.Aggregator.Watch(t.Context(), &corev1.ConfigMapList{}, client.InNamespace("default"))
		require.NoError(t, err)
		t.Cleanup(watcher.Stop)

		seed(t, h.Members["member-a"], "from-a")
		expectEvent(t, watcher.ResultChan(), watch.Added, "from-a", "member-a")

		seed(t, h.Members["member-b"], "from-b")
		expectEvent(t, watcher.ResultChan(), watch.Added, "from-b", "member-b")

		updated := &corev1.ConfigMap{}
		require.NoError(t, h.Members["member-a"].Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "from-a"}, updated))
		updated.Data = map[string]string{"k": "v"}
		require.NoError(t, h.Members["member-a"].Update(t.Context(), updated))
		expectEvent(t, watcher.ResultChan(), watch.Modified, "from-a", "member-a")
	})

	t.Run("virtual cluster label narrows the watch", func(t *testing.T) {
		t.Parallel()

		h := framework.New(t, []string{"member-a", "member-b"}, newAggregatedAPI())

		watcher, err := h.Aggregator.Watch(t.Context(), &corev1.ConfigMapList{},
			client.InNamespace("default"),
			client.MatchingLabels{aggregationv1alpha1.ClusterLabel: "member-b"},
		)
		require.NoError(t, err)
		t.Cleanup(watcher.Stop)

		// created first, but must never arrive: wrong cluster
		seed(t, h.Members["member-a"], "filtered-out")
		seed(t, h.Members["member-b"], "from-b")
		expectEvent(t, watcher.ResultChan(), watch.Added, "from-b", "member-b")
	})

	t.Run("membership change closes the watch", func(t *testing.T) {
		t.Parallel()

		h := framework.New(t, []string{"member-a", "member-b"}, newAggregatedAPI())

		watcher, err := h.Aggregator.Watch(t.Context(), &corev1.ConfigMapList{}, client.InNamespace("default"))
		require.NoError(t, err)
		t.Cleanup(watcher.Stop)

		// prove the watch is live before the membership change
		seed(t, h.Members["member-a"], "pre-change")
		expectEvent(t, watcher.ResultChan(), watch.Added, "pre-change", "member-a")

		// drop member-b from the spec: reconciler removes it, the inner
		// server is swapped and running watches must close
		aggregatedAPI := &aggregationv1alpha1.AggregatedAPI{}
		require.NoError(t, h.Operator.Get(t.Context(), client.ObjectKey{Name: "watch"}, aggregatedAPI))
		aggregatedAPI.Spec.Clusters = aggregatedAPI.Spec.Clusters[:1]
		require.NoError(t, h.Operator.Update(t.Context(), aggregatedAPI))

		expectClosed(t, watcher.ResultChan())
	})
}
