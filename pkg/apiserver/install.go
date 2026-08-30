package apiserver

import (
	"fmt"
	"time"

	apidiscoveryv2 "k8s.io/api/apidiscovery/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured/unstructuredscheme"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/managedfields"
	genericapi "k8s.io/apiserver/pkg/endpoints"
	"k8s.io/apiserver/pkg/endpoints/discovery"
	discoveryendpoint "k8s.io/apiserver/pkg/endpoints/discovery/aggregated"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
)

// installGroupVersion is a single-group version of GenericAPIServer's InstallAPIGroup for unstructured.
// The upstream version uses the scheme's creator which breaks of on the
// kind-less zero values on SSA.
func installGroupVersion(server *genericapiserver.GenericAPIServer, scheme *runtime.Scheme, serializer runtime.NegotiatedSerializer, groupVersion schema.GroupVersion, apiPrefix string, storageMap map[string]rest.Storage) error {
	groupVersionInstall := &genericapi.APIGroupVersion{
		Storage:      storageMap,
		Root:         apiPrefix,
		GroupVersion: groupVersion,

		ParameterCodec:        runtime.NewParameterCodec(scheme),
		Serializer:            serializer,
		Creater:               unstructuredscheme.NewUnstructuredCreator(),
		Convertor:             unstructuredConvertor{},
		ConvertabilityChecker: scheme,
		UnsafeConvertor:       unstructuredConvertor{},
		Defaulter:             unstructuredscheme.NewUnstructuredDefaulter(),
		Typer:                 unstructuredscheme.NewUnstructuredObjectTyper(),
		Namer:                 runtime.Namer(meta.NewAccessor()),

		MetaGroupVersion: &metav1.SchemeGroupVersion,

		EquivalentResourceRegistry: server.EquivalentResourceRegistry,
		Authorizer:                 server.Authorizer,
		TypeConverter:              managedfields.NewDeducedTypeConverter(),
		// 30m is the upstream default and leaving it at zero instantly
		// cancels timeout contexts derived from it
		MinRequestTimeout: 30 * time.Minute,
	}

	discoveryResources, _, err := groupVersionInstall.InstallREST(server.Handler.GoRestfulContainer)
	if err != nil {
		return fmt.Errorf("installing %s: %w", groupVersion, err)
	}

	versionDiscovery := apidiscoveryv2.APIVersionDiscovery{
		Freshness: apidiscoveryv2.DiscoveryFreshnessCurrent,
		Version:   groupVersion.Version,
		Resources: discoveryResources,
	}

	if apiPrefix == genericapiserver.APIGroupPrefix {
		server.AggregatedDiscoveryGroupManager.AddGroupVersion(groupVersion.Group, versionDiscovery)
		apiGroup := metav1.APIGroup{
			Name: groupVersion.Group,
			Versions: []metav1.GroupVersionForDiscovery{{
				GroupVersion: groupVersion.String(),
				Version:      groupVersion.Version,
			}},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: groupVersion.String(),
				Version:      groupVersion.Version,
			},
		}
		server.DiscoveryGroupManager.AddGroup(apiGroup)
		server.Handler.GoRestfulContainer.Add(discovery.NewAPIGroupHandler(server.Serializer, apiGroup).WebService())
		return nil
	}

	server.AggregatedLegacyDiscoveryGroupManager.AddGroupVersion(groupVersion.Group, versionDiscovery)
	// root /api handler enumerating the legacy versions
	addresses := discovery.DefaultAddresses{DefaultAddress: server.ExternalAddress}
	legacyRootHandler := discovery.NewLegacyRootAPIHandler(addresses, server.Serializer, apiPrefix)
	wrapped := discoveryendpoint.WrapAggregatedDiscoveryToHandler(legacyRootHandler, server.AggregatedLegacyDiscoveryGroupManager, server.AggregatedLegacyDiscoveryGroupManager)
	server.Handler.GoRestfulContainer.Add(wrapped.GenerateWebService("/api", metav1.APIVersions{}))
	return nil
}

type unstructuredConvertor struct{}

var _ runtime.ObjectConvertor = unstructuredConvertor{}

func (unstructuredConvertor) Convert(in, out, _ any) error {
	unstructIn, ok := in.(runtime.Unstructured)
	if !ok {
		return fmt.Errorf("input type %T is not unstructured", in)
	}
	unstructOut, ok := out.(runtime.Unstructured)
	if !ok {
		return fmt.Errorf("output type %T is not unstructured", out)
	}
	outGVK := unstructOut.GetObjectKind().GroupVersionKind()
	unstructOut.SetUnstructuredContent(unstructIn.UnstructuredContent())
	if !outGVK.Empty() {
		unstructOut.GetObjectKind().SetGroupVersionKind(outGVK)
	}
	return nil
}

func (unstructuredConvertor) ConvertToVersion(in runtime.Object, target runtime.GroupVersioner) (runtime.Object, error) {
	fromGVK := in.GetObjectKind().GroupVersionKind()
	toGVK, ok := target.KindForGroupVersionKinds([]schema.GroupVersionKind{fromGVK})
	if !ok {
		return nil, fmt.Errorf("%v cannot be converted to target %q", fromGVK, target)
	}
	if toGVK.Version == runtime.APIVersionInternal {
		return in, nil
	}
	out := in.DeepCopyObject()
	out.GetObjectKind().SetGroupVersionKind(toGVK)
	return out, nil
}

func (unstructuredConvertor) ConvertFieldLabel(_ schema.GroupVersionKind, label, value string) (string, string, error) {
	return label, value, nil
}
