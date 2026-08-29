package integration

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aggregationv1alpha1 "github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	"github.com/ntnn/aggregated-apiserver-operator/test/integration/framework"
)

func TestAggregatedConfigMaps(t *testing.T) {
	t.Parallel()

	h := framework.New(t, []string{"member-a", "member-b"}, &aggregationv1alpha1.AggregatedAPI{
		ObjectMeta: metav1.ObjectMeta{Name: "configmaps"},
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
	})

	memberA, memberB := h.Members["member-a"], h.Members["member-b"]
	cms := h.Aggregator
	inDefault := client.InNamespace("default")

	// Seed one shared-name ConfigMap in both members and one unique per member.
	seed := func(t *testing.T, cl client.Client, name string, data map[string]string) {
		t.Helper()
		require.NoError(t,
			cl.Create(
				t.Context(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: "default",
					},
					Data: data,
				},
			),
		)
	}
	seed(t, memberA, "shared", map[string]string{"origin": "member-a"})
	seed(t, memberB, "shared", map[string]string{"origin": "member-b"})
	seed(t, memberA, "only-a", map[string]string{"origin": "member-a"})
	seed(t, memberB, "only-b", map[string]string{"origin": "member-b"})

	t.Run("list merges all clusters and stamps cluster metadata", func(t *testing.T) {
		t.Parallel()

		list := &corev1.ConfigMapList{}
		require.NoError(t, cms.List(t.Context(), list, inDefault))
		require.Len(t, list.Items, 4)

		for _, item := range list.Items {
			cluster := item.Annotations[aggregationv1alpha1.ClusterAnnotation]
			require.NotEmpty(t, cluster, "every item must carry the source-cluster annotation")
			require.Equal(t, cluster, item.Labels[aggregationv1alpha1.ClusterLabel], "virtual label must match the annotation")
		}
	})

	t.Run("list filtered by virtual cluster label", func(t *testing.T) {
		t.Parallel()

		list := &corev1.ConfigMapList{}
		require.NoError(t,
			cms.List(
				t.Context(),
				list,
				inDefault,
				client.MatchingLabels{aggregationv1alpha1.ClusterLabel: "member-a"},
			),
		)
		require.Len(t, list.Items, 2)
		for _, item := range list.Items {
			require.Equal(t, "member-a", item.Annotations[aggregationv1alpha1.ClusterAnnotation])
		}
	})

	t.Run("get unique name returns the single hit", func(t *testing.T) {
		t.Parallel()

		got := &corev1.ConfigMap{}
		require.NoError(t,
			cms.Get(
				t.Context(),
				client.ObjectKey{
					Namespace: "default",
					Name:      "only-b",
				},
				got,
			),
		)
		require.Equal(t, "member-b", got.Annotations[aggregationv1alpha1.ClusterAnnotation])
	})

	t.Run("get ambiguous name conflicts naming candidates", func(t *testing.T) {
		t.Parallel()

		err := cms.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: "shared"}, &corev1.ConfigMap{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "member-a")
		require.Contains(t, err.Error(), "member-b")
	})

	t.Run("create routes via cluster annotation", func(t *testing.T) {
		t.Parallel()

		created := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "created",
				Namespace: "default",
				Annotations: map[string]string{
					aggregationv1alpha1.ClusterAnnotation: "member-a",
				},
			},
			Data: map[string]string{
				"k": "v",
			},
		}
		require.NoError(t,
			cms.Create(
				t.Context(),
				created,
			),
		)
		require.Equal(t, "member-a", created.Annotations[aggregationv1alpha1.ClusterAnnotation])

		// Landed on member-a only, without aggregator-owned metadata.
		remote := &corev1.ConfigMap{}
		require.NoError(t,
			memberA.Get(
				t.Context(),
				client.ObjectKey{
					Namespace: "default",
					Name:      "created",
				},
				remote,
			),
		)
		require.NotContains(t, remote.Annotations, aggregationv1alpha1.ClusterAnnotation)
		require.NotContains(t, remote.Labels, aggregationv1alpha1.ClusterLabel)
		require.True(t,
			apierrors.IsNotFound(
				memberB.Get(
					t.Context(),
					client.ObjectKey{
						Namespace: "default",
						Name:      "created",
					},
					&corev1.ConfigMap{},
				),
			),
		)
	})

	t.Run("create without cluster annotation is rejected", func(t *testing.T) {
		t.Parallel()

		require.Error(t,
			cms.Create(
				t.Context(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "no-target",
						Namespace: "default",
					},
				},
			),
		)
	})

	t.Run("update routes to the owning cluster", func(t *testing.T) {
		t.Parallel()

		seed(t, memberA, "to-update", map[string]string{"k": "old"})

		got := &corev1.ConfigMap{}
		require.NoError(t,
			cms.Get(
				t.Context(),
				client.ObjectKey{
					Namespace: "default",
					Name:      "to-update",
				},
				got,
			),
		)
		got.Data["k"] = "new"
		require.NoError(t, cms.Update(t.Context(), got))

		remote := &corev1.ConfigMap{}
		require.NoError(t,
			memberA.Get(
				t.Context(),
				client.ObjectKey{
					Namespace: "default",
					Name:      "to-update",
				},
				remote,
			),
		)
		require.Equal(t, "new", remote.Data["k"])
	})

	t.Run("patch routes to the owning cluster", func(t *testing.T) {
		t.Parallel()

		seed(t, memberB, "to-patch", map[string]string{"k": "old"})

		require.NoError(t,
			cms.Patch(
				t.Context(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "to-patch",
						Namespace: "default",
					},
				},
				client.RawPatch(
					types.MergePatchType,
					[]byte(`{"data":{"k":"patched"}}`),
				),
			),
		)

		remote := &corev1.ConfigMap{}
		require.NoError(t,
			memberB.Get(
				t.Context(),
				client.ObjectKey{
					Namespace: "default",
					Name:      "to-patch",
				},
				remote,
			),
		)
		require.Equal(t, "patched", remote.Data["k"])
	})

	t.Run("delete routes to the owning cluster", func(t *testing.T) {
		t.Parallel()

		seed(t, memberA, "to-delete", nil)

		require.NoError(t,
			cms.Delete(
				t.Context(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "to-delete",
						Namespace: "default",
					},
				},
			),
		)
		require.True(t,
			apierrors.IsNotFound(
				memberA.Get(
					t.Context(),
					client.ObjectKey{
						Namespace: "default",
						Name:      "to-delete",
					},
					&corev1.ConfigMap{},
				),
			),
		)
	})

	t.Run("delete ambiguous name conflicts", func(t *testing.T) {
		t.Parallel()

		require.Error(t,
			cms.Delete(
				t.Context(),
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "shared",
						Namespace: "default",
					},
				},
			),
		)
	})
}
