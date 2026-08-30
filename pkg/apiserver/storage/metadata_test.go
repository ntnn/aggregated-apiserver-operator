package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

func TestStamp(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		annotations map[string]string
		labels      map[string]string
	}{
		"empty metadata":            {nil, nil},
		"existing annotations kept": {map[string]string{"keep": "me"}, nil},
		"existing labels kept":      {nil, map[string]string{"keep": "me"}},
		"prior stamp overwritten": {
			map[string]string{v1alpha1.ClusterAnnotation: "old"},
			map[string]string{v1alpha1.ClusterLabel: "old"},
		},
	}

	for title, cas := range cases {
		t.Run(title, func(t *testing.T) {
			t.Parallel()

			obj := &unstructured.Unstructured{Object: map[string]any{}}
			obj.SetAnnotations(cas.annotations)
			obj.SetLabels(cas.labels)

			stamp(obj, "east")

			assert.Equal(t, "east", obj.GetAnnotations()[v1alpha1.ClusterAnnotation])
			assert.Equal(t, "east", obj.GetLabels()[v1alpha1.ClusterLabel])
			for key, value := range cas.annotations {
				if key == v1alpha1.ClusterAnnotation {
					continue
				}
				assert.Equal(t, value, obj.GetAnnotations()[key], "pre-existing annotations must survive stamping")
			}
			for key, value := range cas.labels {
				if key == v1alpha1.ClusterLabel {
					continue
				}
				assert.Equal(t, value, obj.GetLabels()[key], "pre-existing labels must survive stamping")
			}
		})
	}
}

func TestSplitClusterSelector(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		selector    string
		wantRemote  string
		matching    []string
		nonMatching []string
	}{
		"nil-equivalent empty selector": {
			selector:   "",
			wantRemote: "",
			matching:   []string{"east", "west"},
		},
		"no cluster term passes selector through": {
			selector:   "app=web",
			wantRemote: "app=web",
			matching:   []string{"east", "west"},
		},
		"cluster equality stripped and matched": {
			selector:    v1alpha1.ClusterLabel + "=east,app=web",
			wantRemote:  "app=web",
			matching:    []string{"east"},
			nonMatching: []string{"west"},
		},
		"cluster in-set stripped and matched": {
			selector:    v1alpha1.ClusterLabel + " in (east,west)",
			wantRemote:  "",
			matching:    []string{"east", "west"},
			nonMatching: []string{"north"},
		},
		"cluster inequality stripped and matched": {
			selector:    v1alpha1.ClusterLabel + "!=east",
			wantRemote:  "",
			matching:    []string{"west"},
			nonMatching: []string{"east"},
		},
	}

	for title, cas := range cases {
		t.Run(title, func(t *testing.T) {
			t.Parallel()

			selector, err := labels.Parse(cas.selector)
			require.NoError(t, err)

			remote, matches := splitClusterSelector(selector)

			if cas.wantRemote == "" {
				assert.True(t, remote == nil || remote.Empty(), "remote selector must be empty")
			} else {
				require.NotNil(t, remote)
				assert.Equal(t, cas.wantRemote, remote.String())
			}
			for _, cluster := range cas.matching {
				assert.True(t, matches(cluster), "cluster %q must match", cluster)
			}
			for _, cluster := range cas.nonMatching {
				assert.False(t, matches(cluster), "cluster %q must not match", cluster)
			}
		})
	}
}

func TestSplitClusterSelector_nilSelector(t *testing.T) {
	t.Parallel()

	remote, matches := splitClusterSelector(nil)

	assert.Nil(t, remote)
	assert.True(t, matches("any"))
}
