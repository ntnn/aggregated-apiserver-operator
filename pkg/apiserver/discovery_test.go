package apiserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

var testDiscovery = []*metav1.APIResourceList{
	{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{
			{Name: "configmaps", SingularName: "configmap", Kind: "ConfigMap", Namespaced: true},
			{Name: "secrets", SingularName: "secret", Kind: "Secret", Namespaced: true},
		},
	},
	{
		GroupVersion: "apps/v1",
		APIResources: []metav1.APIResource{
			{Name: "deployments", SingularName: "deployment", Kind: "Deployment", Namespaced: true},
			{Name: "deployments/status", Kind: "Deployment", Namespaced: true},
		},
	},
}

func TestFilter(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		selectors []v1alpha1.APISelector
		want      []string
	}{
		"wildcard group serves everything": {
			[]v1alpha1.APISelector{{Group: "*"}},
			[]string{"configmaps", "secrets", "deployments"},
		},
		"group pins resources to that group": {
			[]v1alpha1.APISelector{{Group: "apps"}},
			[]string{"deployments"},
		},
		"resource names narrow the group": {
			[]v1alpha1.APISelector{{Group: "", Resources: []string{"configmaps"}}},
			[]string{"configmaps"},
		},
		"wildcard resource in group": {
			[]v1alpha1.APISelector{{Group: "", Resources: []string{"*"}}},
			[]string{"configmaps", "secrets"},
		},
		"version mismatch excludes": {
			[]v1alpha1.APISelector{{Group: "apps", Versions: []string{"v1beta1"}}},
			nil,
		},
		"no selector matches nothing": {
			nil,
			nil,
		},
	}

	for title, cas := range cases {
		t.Run(title, func(t *testing.T) {
			t.Parallel()

			all, err := FromDiscovery(testDiscovery)
			require.NoError(t, err)
			served := Filter(all, cas.selectors)

			var names []string
			for _, resource := range served {
				names = append(names, resource.GVR.Resource)
			}
			assert.ElementsMatch(t, cas.want, names)
			for _, resource := range served {
				assert.NotContains(t, resource.GVR.Resource, "/", "subresources must never be served directly")
			}
		})
	}
}

func TestUnion(t *testing.T) {
	t.Parallel()

	configmaps := ServedResource{
		GVR:        schema.GroupVersionResource{Version: "v1", Resource: "configmaps"},
		Kind:       schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"},
		Namespaced: true,
	}

	t.Run("shared GVR records both clusters", func(t *testing.T) {
		t.Parallel()

		merged, err := Union(map[string][]ServedResource{
			"east": {configmaps},
			"west": {configmaps},
		})
		require.NoError(t, err)
		require.Len(t, merged, 1)
		assert.Equal(t, []string{"east", "west"}, merged[0].Clusters)
	})

	t.Run("kind conflict errors", func(t *testing.T) {
		t.Parallel()

		conflicting := configmaps
		conflicting.Kind.Kind = "NotAConfigMap"

		_, err := Union(map[string][]ServedResource{
			"east": {configmaps},
			"west": {conflicting},
		})
		assert.Error(t, err)
	})

	t.Run("scope conflict errors", func(t *testing.T) {
		t.Parallel()

		conflicting := configmaps
		conflicting.Namespaced = false

		_, err := Union(map[string][]ServedResource{
			"east": {configmaps},
			"west": {conflicting},
		})
		assert.Error(t, err)
	})
}
