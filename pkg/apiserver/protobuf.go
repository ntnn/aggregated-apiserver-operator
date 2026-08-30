package apiserver

import (
	"fmt"
	"io"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/protobuf"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// typedScheme is the scheme containing APIs that are supported to be
// de-/encoded from/to protobuf.
var typedScheme *runtime.Scheme

func init() {
	typedScheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(typedScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(typedScheme))
}

// protoRoundTripSerializer provides protobuf support for the apiserver.
type protoRoundTripSerializer struct {
	proto runtime.Serializer
}

var _ runtime.Serializer = protoRoundTripSerializer{}

func newProtoRoundTripSerializer() protoRoundTripSerializer {
	return protoRoundTripSerializer{
		proto: protobuf.NewSerializer(typedScheme, typedScheme),
	}
}

func newProtoRoundTripStreamSerializer() *runtime.StreamSerializerInfo {
	return &runtime.StreamSerializerInfo{
		EncodesAsText: false,
		Serializer: protoRoundTripSerializer{
			proto: protobuf.NewRawSerializer(typedScheme, typedScheme),
		},
		Framer: protobuf.LengthDelimitedFramer,
	}
}

func (s protoRoundTripSerializer) Identifier() runtime.Identifier {
	return "protoRoundTrip:" + s.proto.Identifier()
}

func (s protoRoundTripSerializer) Decode(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error) {
	typed, gvk, err := s.proto.Decode(data, defaults, nil)
	if err != nil {
		return nil, gvk, err
	}

	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(typed)
	if err != nil {
		return nil, gvk, fmt.Errorf("converting %s to unstructured: %w", gvk, err)
	}

	u, ok := into.(*unstructured.Unstructured)
	if !ok {
		u = &unstructured.Unstructured{}
	}
	u.SetUnstructuredContent(content)
	u.SetGroupVersionKind(*gvk)
	return u, gvk, nil
}

func (s protoRoundTripSerializer) Encode(obj runtime.Object, w io.Writer) error {
	u, ok := obj.(runtime.Unstructured)
	if !ok {
		return s.proto.Encode(obj, w)
	}

	gvk := obj.GetObjectKind().GroupVersionKind()
	typed, err := typedScheme.New(gvk)
	if err != nil {
		return runtime.NewNotRegisteredErrForKind(typedScheme.Name(), gvk)
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.UnstructuredContent(), typed); err != nil {
		return fmt.Errorf("converting %s from unstructured: %w", gvk, err)
	}
	typed.GetObjectKind().SetGroupVersionKind(gvk)
	return s.proto.Encode(typed, w)
}
