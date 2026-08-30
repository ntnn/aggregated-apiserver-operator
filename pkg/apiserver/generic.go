package apiserver

import (
	"fmt"
	"net"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/dynamic"
	restclient "k8s.io/client-go/rest"
	basecompatibility "k8s.io/component-base/compatibility"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver/storage"
)

// newGenericServer builds a new [genericapiserver.GenericAPIServer] for the given resources and clusters.
func newGenericServer(hostname string, port int, resources []ServedResource, clusters map[string]dynamic.Interface, done <-chan struct{}) (*genericapiserver.GenericAPIServer, error) {
	config := genericapiserver.NewConfig(newServerScheme())
	config.EffectiveVersion = basecompatibility.NewEffectiveVersionFromString("1.0", "", "")

	// TODO(ntnn): no authn yet, so authorization is allow-all
	config.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

	// configure openapi v2 and v3
	config.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(
		unstructuredOpenAPIDefinitions,
		openapi.NewDefinitionNamer(runtime.NewScheme()),
	)
	config.OpenAPIV3Config.Info.Title = "api-aggregator"
	config.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(
		unstructuredOpenAPIDefinitions,
		openapi.NewDefinitionNamer(runtime.NewScheme()),
	)
	config.OpenAPIConfig.Info.Title = "api-aggregator"

	// The apiserver serves everything as unstructured, which breaks the
	// default namer because it assumed that unstructured means a CRD is
	// being served and rejects unstructured for APIs with an empty
	// group (so core APIs).
	// Instead a custom namer is used that returns the
	// x-kubernetes-group-version-kind extension, which then skips the
	// entire breaking branch.
	namer := &unstructuredNamer{resources: resources}
	config.OpenAPIV3Config.GetDefinitionName = namer.GetDefinitionName
	config.OpenAPIConfig.GetDefinitionName = namer.GetDefinitionName

	config.ExternalAddress = net.JoinHostPort(hostname, strconv.Itoa(port))
	// never used: no post-start hooks dial back, but New() requires it
	config.LoopbackClientConfig = &restclient.Config{Host: "https://" + config.ExternalAddress}

	server, err := config.Complete(nil).New("api-aggregator", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("building generic apiserver: %w", err)
	}
	if err := installResources(server, resources, clusters, done); err != nil {
		return nil, err
	}
	return server, nil
}

func installResources(server *genericapiserver.GenericAPIServer, resources []ServedResource, clusters map[string]dynamic.Interface, done <-chan struct{}) error {
	scheme, codecs := newScheme(resources)
	negotiated := defaultingNegotiatedSerializer{codecs}

	byGroupVersion := map[schema.GroupVersion]map[string]rest.Storage{}
	for _, resource := range resources {
		resourceClusters := make(map[string]dynamic.Interface, len(resource.Clusters))
		for _, cluster := range resource.Clusters {
			client, ok := clusters[cluster]
			if !ok {
				return fmt.Errorf("no client for cluster %q serving %s", cluster, resource.GVR)
			}
			resourceClusters[cluster] = client
		}
		store, err := storage.New(storage.Options{
			GVR:        resource.GVR,
			Kind:       resource.Kind,
			Namespaced: resource.Namespaced,
			Singular:   resource.Singular,
			Clusters:   resourceClusters,
			Done:       done,
		})
		if err != nil {
			return fmt.Errorf("building storage for %s: %w", resource.GVR, err)
		}
		gv := resource.GVR.GroupVersion()
		if byGroupVersion[gv] == nil {
			byGroupVersion[gv] = map[string]rest.Storage{}
		}
		byGroupVersion[gv][resource.GVR.Resource] = store
		if resource.Status {
			byGroupVersion[gv][resource.GVR.Resource+"/status"] = storage.NewStatus(store)
		}
	}

	for gv, storageMap := range byGroupVersion {
		apiPrefix := genericapiserver.APIGroupPrefix
		if gv.Group == "" {
			apiPrefix = genericapiserver.DefaultLegacyAPIPrefix
		}
		if err := installGroupVersion(server, scheme, negotiated, gv, apiPrefix, storageMap); err != nil {
			return err
		}
	}
	return nil
}
