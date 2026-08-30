package aggregatedapi

import (
	"context"
	"fmt"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func kubeconfigSecretName(aggregatedAPI string) string {
	return aggregatedAPI + "-kubeconfig"
}

func kubeconfigForURL(url string) ([]byte, error) {
	kubeconfig := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"aggregated": {
				Server:                url,
				InsecureSkipTLSVerify: true,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"aggregated": {
				Token: "unused",
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"aggregated": {
				Cluster:  "aggregated",
				AuthInfo: "aggregated",
			},
		},
		CurrentContext: "aggregated",
	}
	raw, err := clientcmd.Write(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("encoding kubeconfig: %w", err)
	}
	return raw, nil
}

func (r *reconciler) url() string {
	if r.opts.URL != "" {
		return r.opts.URL
	}
	return r.opts.Server.URL()
}

func (r *reconciler) applyKubeconfigSecret(ctx context.Context) error {
	raw, err := kubeconfigForURL(r.url())
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubeconfigSecretName(r.aggregatedAPI.Name),
			Namespace: r.aggregatedAPI.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(
					r.aggregatedAPI,
					v1alpha1.GroupVersion.WithKind("AggregatedAPI"),
				),
			},
		},
		Data: map[string][]byte{
			"kubeconfig": raw,
		},
	}

	if err := r.opts.Client.Patch(
		ctx,
		secret,
		client.Apply, //nolint:staticcheck // yes its deprecated, no i don't want to roundtrip over unstructured
		client.FieldOwner(ControllerName),
		client.ForceOwnership,
	); err != nil {
		return fmt.Errorf("applying Secret: %w", err)
	}
	return nil
}
