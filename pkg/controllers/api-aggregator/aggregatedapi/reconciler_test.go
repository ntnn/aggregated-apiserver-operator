package aggregatedapi

import (
	"testing"

	"github.com/go-logr/logr/testr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
)

// newTestReconciler wires a reconciler over a fake host client and a
// real (not running) Server; discovery and dynamic clients are static doubles.
func newTestReconciler(t *testing.T, objects ...runtime.Object) *reconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	server, err := apiserver.New(apiserver.Options{Hostname: "127.0.0.1", Port: 6443})
	require.NoError(t, err)

	opts := Options{
		AggregatedAPI: "test",
		Namespace:     "default",
		Server:        server,
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithRuntimeObjects(objects...).
			WithStatusSubresource(&v1alpha1.AggregatedAPI{}).
			Build(),
		DiscoveryClient: func(*rest.Config) (discovery.DiscoveryInterface, error) {
			return fakeDiscoveryClient(), nil
		},
		DynamicClient: func(*rest.Config) (dynamic.Interface, error) {
			return fakeDynamicClient(), nil
		},
	}
	require.NoError(t, opts.validate())

	return &reconciler{opts: opts, log: testr.New(t)}
}

func fakeDynamicClient() dynamic.Interface {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "DeploymentList"}, &unstructured.UnstructuredList{})
	return dynamicfake.NewSimpleDynamicClient(scheme)
}

// fakeDiscoveryClient serves a static discovery document with apps/v1 deployments.
func fakeDiscoveryClient() discovery.DiscoveryInterface {
	fakeClient := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	fakeClient.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", SingularName: "deployment", Kind: "Deployment", Namespaced: true},
			},
		},
	}
	return fakeClient
}

func kubeconfigSecret(t *testing.T, name string) *corev1.Secret {
	t.Helper()

	raw, err := clientcmd.Write(clientcmdapi.Config{
		Clusters:  map[string]*clientcmdapi.Cluster{"c": {Server: "https://127.0.0.1:1"}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"c": {Token: "t"}},
		Contexts: map[string]*clientcmdapi.Context{
			"c": {Cluster: "c", AuthInfo: "c"},
		},
		CurrentContext: "c",
	})
	require.NoError(t, err)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{"kubeconfig": raw},
	}
}

func aggregatedAPI(clusters ...string) *v1alpha1.AggregatedAPI {
	spec := v1alpha1.AggregatedAPISpec{}
	for _, cluster := range clusters {
		spec.Clusters = append(spec.Clusters, v1alpha1.AggregatedCluster{
			Access: v1alpha1.ClusterAccess{KubeconfigName: cluster},
			APIs:   []v1alpha1.APISelector{{Group: "*"}},
		})
	}
	return &v1alpha1.AggregatedAPI{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Generation: 3},
		Spec:       spec,
	}
}

func TestReconciler_reconcile(t *testing.T) {
	t.Parallel()

	t.Run("registers spec clusters and sets Ready with URL", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t,
			aggregatedAPI("member-a", "member-b"),
			kubeconfigSecret(t, "member-a"),
			kubeconfigSecret(t, "member-b"),
		)

		_, err := r.reconcile(t.Context())
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"member-a", "member-b"}, r.opts.Server.Clusters())

		updated := &v1alpha1.AggregatedAPI{}
		require.NoError(t, r.opts.Client.Get(t.Context(), objectKey("test"), updated))
		cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.AggregatedAPIConditionReady)
		require.NotNil(t, cond)

		assert.Equal(t, metav1.ConditionTrue, cond.Status)
		assert.Equal(t, "ClustersRegistered", cond.Reason)
		assert.Equal(t, int64(3), cond.ObservedGeneration)
		assert.Equal(t, r.opts.Server.URL(), updated.Status.URL)
	})

	t.Run("removes clusters that left the spec", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t,
			aggregatedAPI("member-a"),
			kubeconfigSecret(t, "member-a"),
		)
		require.NoError(t, r.opts.Server.SetCluster("stale", fakeDynamicClient(), fakeDiscoveryClient(), []v1alpha1.APISelector{{Group: "apps"}}))

		_, err := r.reconcile(t.Context())
		require.NoError(t, err)

		assert.Equal(t, []string{"member-a"}, r.opts.Server.Clusters())
	})

	t.Run("missing Secret sets Ready False and returns the error", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t, aggregatedAPI("member-a"))

		_, err := r.reconcile(t.Context())
		require.Error(t, err)

		updated := &v1alpha1.AggregatedAPI{}
		require.NoError(t, r.opts.Client.Get(t.Context(), objectKey("test"), updated))
		cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.AggregatedAPIConditionReady)
		require.NotNil(t, cond)

		assert.Equal(t, metav1.ConditionFalse, cond.Status)
		assert.Equal(t, "ClusterRegistrationFailed", cond.Reason)
		assert.Contains(t, cond.Message, "member-a")
	})

	t.Run("failed registration still registers the healthy clusters", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t,
			aggregatedAPI("member-a", "member-b"),
			kubeconfigSecret(t, "member-b"),
		)

		_, err := r.reconcile(t.Context())
		require.Error(t, err)
		assert.Contains(t, r.opts.Server.Clusters(), "member-b", "one failing cluster must not block the others")
	})

	t.Run("stale Ready False clears on a later success", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t, aggregatedAPI("member-a"))

		_, err := r.reconcile(t.Context())
		require.Error(t, err)

		require.NoError(t, r.opts.Client.Create(t.Context(), kubeconfigSecret(t, "member-a")))
		r2 := &reconciler{opts: r.opts, log: r.log}
		_, err = r2.reconcile(t.Context())
		require.NoError(t, err)

		updated := &v1alpha1.AggregatedAPI{}
		require.NoError(t, r.opts.Client.Get(t.Context(), objectKey("test"), updated))
		cond := meta.FindStatusCondition(updated.Status.Conditions, v1alpha1.AggregatedAPIConditionReady)
		require.NotNil(t, cond)
		assert.Equal(t, metav1.ConditionTrue, cond.Status, "a transient failure must not leave Ready stuck at False")
	})

	t.Run("deleted AggregatedAPI is a clean no-op", func(t *testing.T) {
		t.Parallel()

		r := newTestReconciler(t)

		_, err := r.reconcile(t.Context())
		assert.NoError(t, err)
	})
}

func objectKey(name string) client.ObjectKey {
	return client.ObjectKey{Name: name}
}
