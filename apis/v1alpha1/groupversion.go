package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the group and version of the types in this package.
var GroupVersion = schema.GroupVersion{Group: "aggregation.ntnn.dev", Version: "v1alpha1"}

// SchemeBuilder registers the types of this package into a scheme.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds the types of this package to a scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&AggregatedAPI{},
		&AggregatedAPIList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
