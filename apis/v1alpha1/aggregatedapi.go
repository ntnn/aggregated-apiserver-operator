package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// AggregatedAPIConditionReady reports whether all spec clusters are registered.
	AggregatedAPIConditionReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// AggregatedAPI is one endpoint aggregating a number of kube APIs.
type AggregatedAPI struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AggregatedAPISpec   `json:"spec"`
	Status AggregatedAPIStatus `json:"status,omitempty"`
}

// AggregatedAPISpec declares the clusters and APIs being aggregated.
type AggregatedAPISpec struct {
	// Clusters lists the clusters being aggregated.
	// +required
	Clusters []AggregatedCluster `json:"clusters"`
}

// AggregatedCluster selects one member cluster and the APIs served
// from it.
type AggregatedCluster struct {
	// Access is how the cluster is accessed.
	// +required
	Access ClusterAccess `json:"access"`

	// APIs selects which APIs of the cluster are served.
	// A single entry with group "*" serves all discovered APIs.
	// +required
	APIs []APISelector `json:"apis"`
}

// ClusterAccess defines how to retrieve a kubeconfig to access a cluster.
type ClusterAccess struct {
	// KubeconfigName references a kubeconfig Secret by name.
	// +required
	KubeconfigName string `json:"name,omitempty"`
}

// APISelector selects APIs by group, resource and version.
type APISelector struct {
	// Group is the API group; "*" matches all groups.
	Group string `json:"group"`

	// Resources are plural resource names; "*" or empty matches all
	// resources in the group.
	Resources []string `json:"resources,omitempty"`

	// Versions restricts served versions; empty means all discovered
	// versions.
	Versions []string `json:"versions,omitempty"`
}

// AggregatedAPIStatus reports the state of the aggregation endpoint.
type AggregatedAPIStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// URL is the endpoint serving this aggregated API.
	URL string `json:"url,omitempty"`
}

// +kubebuilder:object:root=true

// AggregatedAPIList is a list of AggregatedAPI objects.
type AggregatedAPIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []AggregatedAPI `json:"items"`
}
