package apiserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()

	server, err := New(Options{})
	require.NoError(t, err)
	return server
}

func fakeDynamic(t *testing.T) dynamic.Interface {
	t.Helper()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DeploymentList"}, &unstructured.UnstructuredList{})
	return dynamicfake.NewSimpleDynamicClient(scheme)
}

// fakeDiscovery serves a static discovery document with apps/v1 deployments.
func fakeDiscovery(t *testing.T) discovery.DiscoveryInterface {
	t.Helper()

	fake := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	fake.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", SingularName: "deployment", Kind: "Deployment", Namespaced: true},
			},
		},
	}
	return fake
}

func discoveryPaths(t *testing.T, server *Server, path string) int {
	t.Helper()

	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, http.NoBody))
	return recorder.Code
}

func TestServer_SetCluster(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)

	t.Run("empty server serves nothing", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, discoveryPaths(t, server, "/apis/apps/v1"))
	})

	t.Run("SetCluster starts serving the resource", func(t *testing.T) {
		require.NoError(t, server.SetCluster("east", fakeDynamic(t), fakeDiscovery(t), []v1alpha1.APISelector{{Group: "apps"}}))
		assert.Equal(t, http.StatusOK, discoveryPaths(t, server, "/apis/apps/v1"))
	})

	t.Run("RemoveCluster of the last cluster stops serving", func(t *testing.T) {
		require.NoError(t, server.RemoveCluster("east"))
		assert.Equal(t, http.StatusNotFound, discoveryPaths(t, server, "/apis/apps/v1"))
	})
}
