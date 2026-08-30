// Package framework holds utility functions for the integration tests.
package framework

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	kcp "github.com/ntnn/kcp-testcontainer"
	"github.com/stretchr/testify/require"
	tc "github.com/testcontainers/testcontainers-go"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"

	aggregationv1alpha1 "github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	apiserverpkg "github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/api-aggregator/aggregatedapi"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/api-aggregator/config"
	_ "github.com/ntnn/aggregated-apiserver-operator/pkg/register"
)

const kcpImage = "ghcr.io/kcp-dev/kcp:latest"

// Harness is a per-test environment.
type Harness struct {
	Kcp        *kcp.Container
	Operator   client.Client
	Members    map[string]client.Client
	Aggregator client.WithWatch
}

// New creates a new Harness with a kcp workspace as the control plane
// and one child workspace for each member.
func New(t *testing.T, members []string, aggregatedAPI *aggregationv1alpha1.AggregatedAPI) *Harness {
	t.Helper()

	container, err := kcp.Run(t.Context(), kcpImage)
	require.NoError(t, err)
	tc.CleanupContainer(t, container)

	prefix := fmt.Sprintf("%x", sha256.Sum256([]byte(t.Name())))[:16] + "-"
	operatorPath, err := container.CreateWorkspaceGenerateName(t.Context(), "root", prefix)
	require.NoError(t, err)

	operator, err := container.Client(t.Context(), operatorPath, client.Options{})
	require.NoError(t, err)

	installCRD(t, operator)

	h := &Harness{
		Kcp:      container,
		Operator: operator,
		Members:  map[string]client.Client{},
	}

	for _, member := range members {
		memberPath := operatorPath + ":" + member
		require.NoError(t, container.CreateWorkspace(t.Context(), memberPath))

		cl, err := container.Client(t.Context(), memberPath, client.Options{})
		require.NoError(t, err)
		h.Members[member] = cl

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      member,
				Namespace: "default",
			},
			Data: map[string][]byte{
				"kubeconfig": rest2kubeconfig(t, container, memberPath),
			},
		}
		require.NoError(t, operator.Create(t.Context(), secret))
	}

	aggregatedAPI.Namespace = "default"
	require.NoError(t, operator.Create(t.Context(), aggregatedAPI))

	hostConfig, err := container.RESTConfig(t.Context(), operatorPath)
	require.NoError(t, err)

	ctrllog.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(testWriter{t})))

	tr := true
	mgr, err := ctrl.NewManager(hostConfig, manager.Options{
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: &tr},
	})
	require.NoError(t, err)

	port := freePort(t)
	runCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		server, err := apiserverpkg.New(apiserverpkg.Options{
			Hostname: "127.0.0.1",
			Port:     port,
		})
		if err != nil {
			t.Errorf("api-aggregator server: %v", err)
			return
		}
		if err := config.Setup(mgr, config.Options{
			Server: server,
			AggregatedAPI: aggregatedapi.Options{
				AggregatedAPI: aggregatedAPI.Name,
				Namespace:     aggregatedAPI.Namespace,
			},
		}); err != nil {
			t.Errorf("api-aggregator setup: %v", err)
			return
		}
		group, groupCtx := errgroup.WithContext(runCtx)
		group.Go(func() error { return mgr.Start(groupCtx) })
		group.Go(func() error { return server.Run(groupCtx) })
		if err := group.Wait(); err != nil && runCtx.Err() == nil {
			t.Errorf("api-aggregator exited: %v", err)
		}
	}()

	aggregatorConfig := &rest.Config{
		Host: fmt.Sprintf("https://127.0.0.1:%d", port),
		// aggregator serves unstructured JSON only, no protobuf
		ContentConfig: rest.ContentConfig{
			ContentType:        "application/json",
			AcceptContentTypes: "application/json",
		},
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: true,
		},
	}
	aggregator, err := client.NewWithWatch(aggregatorConfig, client.Options{})
	require.NoError(t, err)
	h.Aggregator = aggregator

	// Aggregator ready once the endpoint answers a list.
	require.NoError(t,
		wait.PollUntilContextTimeout(t.Context(), 250*time.Millisecond, time.Minute, true,
			func(ctx context.Context) (bool, error) {
				err := aggregator.List(ctx, &corev1.ConfigMapList{}, client.InNamespace("default"))
				return err == nil, nil
			},
		),
	)

	return h
}

func installCRD(t *testing.T, cl client.Client) {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "aggregation.ntnn.dev_aggregatedapis.yaml"))
	require.NoError(t, err)
	crd := &apiextensionsv1.CustomResourceDefinition{}
	require.NoError(t, yaml.Unmarshal(raw, crd))
	require.NoError(t, cl.Create(t.Context(), crd))

	require.NoError(t, wait.PollUntilContextTimeout(t.Context(), 100*time.Millisecond, time.Minute, true,
		func(ctx context.Context) (bool, error) {
			current := &apiextensionsv1.CustomResourceDefinition{}
			if err := cl.Get(ctx, client.ObjectKeyFromObject(crd), current); err != nil {
				return false, err
			}
			for _, cond := range current.Status.Conditions {
				if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}))
}

func rest2kubeconfig(t *testing.T, container *kcp.Container, path string) []byte {
	t.Helper()

	restConfig, err := container.RESTConfig(t.Context(), path)
	require.NoError(t, err)

	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"default": {
				Server:                   restConfig.Host,
				CertificateAuthorityData: restConfig.CAData,
				InsecureSkipTLSVerify:    restConfig.Insecure,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"default": {
				Token: restConfig.BearerToken,
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"default": {
				Cluster:  "default",
				AuthInfo: "default",
			},
		},
		CurrentContext: "default",
	}
	raw, err := clientcmd.Write(kubeconfig)
	require.NoError(t, err)
	return raw
}

// testWriter routes controller-runtime logs into the test log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func freePort(t *testing.T) int {
	t.Helper()

	l, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}
