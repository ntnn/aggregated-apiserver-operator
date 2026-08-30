package apiserver

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// ServedResource is one GVR and the clusters serving it.
type ServedResource struct {
	GVR        schema.GroupVersionResource
	Kind       schema.GroupVersionKind
	Namespaced bool
	Singular   string
	Clusters   []string
}

// FromDiscovery converts discovery output to ServedResources; subresources are skipped.
func FromDiscovery(resourceLists []*metav1.APIResourceList) ([]ServedResource, error) {
	var resources []ServedResource
	for _, list := range resourceLists {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			return nil, fmt.Errorf("parsing discovered groupVersion %q: %w", list.GroupVersion, err)
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") {
				continue
			}
			resources = append(resources, ServedResource{
				GVR:        gv.WithResource(resource.Name),
				Kind:       gv.WithKind(resource.Kind),
				Namespaced: resource.Namespaced,
				Singular:   resource.SingularName,
			})
		}
	}
	return resources, nil
}

// Filter keeps the resources matching any of the API selectors.
func Filter(resources []ServedResource, selectors []v1alpha1.APISelector) []ServedResource {
	var filtered []ServedResource
	for _, resource := range resources {
		if matches(resource.GVR, selectors) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

func matches(gvr schema.GroupVersionResource, selectors []v1alpha1.APISelector) bool {
	for _, selector := range selectors {
		if selector.Group != "*" && selector.Group != gvr.Group {
			continue
		}
		if len(selector.Versions) > 0 && !slices.Contains(selector.Versions, gvr.Version) {
			continue
		}
		if len(selector.Resources) > 0 && !slices.Contains(selector.Resources, "*") && !slices.Contains(selector.Resources, gvr.Resource) {
			continue
		}
		return true
	}
	return false
}

// Union merges per-cluster served resources; kind/scope conflicts error.
func Union(byCluster map[string][]ServedResource) ([]ServedResource, error) {
	merged := map[schema.GroupVersionResource]*ServedResource{}
	for cluster, resources := range byCluster {
		for _, resource := range resources {
			existing, ok := merged[resource.GVR]
			if !ok {
				resource.Clusters = []string{cluster}
				merged[resource.GVR] = &resource
				continue
			}
			if existing.Kind != resource.Kind || existing.Namespaced != resource.Namespaced {
				return nil, fmt.Errorf("clusters disagree on %s: kind/scope conflict between %v and %q", resource.GVR, existing.Clusters, cluster)
			}
			existing.Clusters = append(existing.Clusters, cluster)
		}
	}

	result := make([]ServedResource, 0, len(merged))
	for _, resource := range merged {
		sort.Strings(resource.Clusters)
		result = append(result, *resource)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GVR.String() < result[j].GVR.String() })
	return result, nil
}
