package apiserver

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	openapicommon "k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// newScheme registers every served kind as Unstructured plus per-GV meta types.
func newScheme(resources []ServedResource) (*runtime.Scheme, serializer.CodecFactory) {
	scheme := runtime.NewScheme()
	// installer resolves some option kinds against core v1 regardless of group
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})
	for _, resource := range resources {
		scheme.AddKnownTypeWithName(resource.Kind, &unstructured.Unstructured{})
		scheme.AddKnownTypeWithName(
			resource.Kind.GroupVersion().WithKind(resource.Kind.Kind+"List"),
			&unstructured.UnstructuredList{},
		)
		metav1.AddToGroupVersion(scheme, resource.Kind.GroupVersion())
	}
	return scheme, serializer.NewCodecFactory(scheme)
}

// newServerScheme covers objects outside served groups (Status, discovery).
func newServerScheme() serializer.CodecFactory {
	scheme := runtime.NewScheme()
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})
	return serializer.NewCodecFactory(scheme)
}

// unstructuredOpenAPIDefinitions serves free-form objects; SSA needs a definition per type.
func unstructuredOpenAPIDefinitions(_ openapicommon.ReferenceCallback) map[string]openapicommon.OpenAPIDefinition {
	freeForm := openapicommon.OpenAPIDefinition{
		Schema: spec.Schema{
			SchemaProps: spec.SchemaProps{
				Type: []string{"object"},
			},
			VendorExtensible: spec.VendorExtensible{
				Extensions: spec.Extensions{"x-kubernetes-preserve-unknown-fields": true},
			},
		},
	}
	return map[string]openapicommon.OpenAPIDefinition{
		"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.Unstructured":     freeForm,
		"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured.UnstructuredList": freeForm,
	}
}
