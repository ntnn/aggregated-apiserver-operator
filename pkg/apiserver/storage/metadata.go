package storage

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// stamp marks obj with its source cluster.
func stamp(obj *unstructured.Unstructured, cluster string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[v1alpha1.ClusterAnnotation] = cluster
	obj.SetAnnotations(annotations)

	objLabels := obj.GetLabels()
	if objLabels == nil {
		objLabels = map[string]string{}
	}
	objLabels[v1alpha1.ClusterLabel] = cluster
	obj.SetLabels(objLabels)
}

// strip removes aggregator-owned metadata before writing to a member.
func strip(obj *unstructured.Unstructured) {
	annotations := obj.GetAnnotations()
	delete(annotations, v1alpha1.ClusterAnnotation)
	obj.SetAnnotations(annotations)

	objLabels := obj.GetLabels()
	delete(objLabels, v1alpha1.ClusterLabel)
	obj.SetLabels(objLabels)
}

// splitClusterSelector strips cluster-label terms from selector, returning the remote selector and a cluster predicate.
func splitClusterSelector(selector labels.Selector) (labels.Selector, func(cluster string) bool) {
	match := func(string) bool {
		return true
	}
	if selector == nil || selector.Empty() {
		return selector, match
	}

	requirements, selectable := selector.Requirements()
	if !selectable {
		return selector, match
	}

	remote := labels.NewSelector()
	clusterReqs := labels.NewSelector()
	for _, requirement := range requirements {
		if requirement.Key() == v1alpha1.ClusterLabel {
			clusterReqs = clusterReqs.Add(requirement)
			continue
		}
		remote = remote.Add(requirement)
	}
	if clusterReqs.Empty() {
		return selector, match
	}

	match = func(cluster string) bool {
		return clusterReqs.Matches(labels.Set{v1alpha1.ClusterLabel: cluster})
	}
	return remote, match
}
