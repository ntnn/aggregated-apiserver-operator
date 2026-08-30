package apiserver

import (
	"encoding/json"

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

type defaultingNegotiatedSerializer struct {
	serializer.CodecFactory
}

func (f defaultingNegotiatedSerializer) DecoderToVersion(decoder runtime.Decoder, gv runtime.GroupVersioner) runtime.Decoder {
	return defaultingDecoder{f.CodecFactory.DecoderToVersion(decoder, gv)}
}

type defaultingDecoder struct {
	delegate runtime.Decoder
}

func (d defaultingDecoder) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	obj, gvk, err := d.delegate.Decode(data, defaults, into)
	if err == nil || defaults == nil || defaults.Empty() {
		return obj, gvk, err
	}
	if !runtime.IsMissingKind(err) && !runtime.IsMissingVersion(err) {
		return obj, gvk, err
	}

	patched := map[string]any{}
	if err := json.Unmarshal(data, &patched); err != nil {
		return obj, gvk, err
	}
	// default the gvk for requests that are missing them.
	// gvk cannot be inferred because this apiserver works with
	// unstructured to handle all APIs
	patched["apiVersion"] = defaults.GroupVersion().String()
	patched["kind"] = defaults.Kind
	data, marshalErr := json.Marshal(patched)
	if marshalErr != nil {
		return obj, gvk, err
	}
	return d.delegate.Decode(data, defaults, into)
}

// unstructuredOpenAPIDefinitions serves free-form objects
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
