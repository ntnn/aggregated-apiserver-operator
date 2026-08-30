package aggregatedapi

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
)

// reconciler holds the state of a single reconcile pass.
type reconciler struct {
	opts Options
	log  logr.Logger

	old           *v1alpha1.AggregatedAPI
	aggregatedAPI *v1alpha1.AggregatedAPI
}

func (r *reconciler) reconcile(ctx context.Context) (ctrl.Result, error) {
	if cont, err := r.fetchAggregatedAPI(ctx); err != nil || !cont {
		return ctrl.Result{}, err
	}

	err := r.run(ctx)
	return ctrl.Result{}, errors.Join(err, r.commitStatus(ctx))
}

func (r *reconciler) fetchAggregatedAPI(ctx context.Context) (bool, error) {
	aggregatedAPI := &v1alpha1.AggregatedAPI{}
	if err := r.opts.Client.Get(ctx, types.NamespacedName{Name: r.opts.AggregatedAPI}, aggregatedAPI); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting AggregatedAPI: %w", err)
	}
	r.old, r.aggregatedAPI = aggregatedAPI, aggregatedAPI.DeepCopy()
	return true, nil
}

func (r *reconciler) run(ctx context.Context) error {
	specClusters := make([]string, 0, len(r.aggregatedAPI.Spec.Clusters))

	var failures []error
	for _, cluster := range r.aggregatedAPI.Spec.Clusters {
		name := cluster.Access.KubeconfigName
		specClusters = append(specClusters, name)
		if err := r.registerCluster(ctx, name, cluster); err != nil {
			failures = append(failures, fmt.Errorf("cluster %q: %w", name, err))
		}
	}

	// clusters no longer in spec are removed from the server
	for _, name := range r.opts.Server.Clusters() {
		if slices.Contains(specClusters, name) {
			continue
		}
		if err := r.opts.Server.RemoveCluster(name); err != nil {
			failures = append(failures, fmt.Errorf("removing cluster %q: %w", name, err))
		}
	}

	err := errors.Join(failures...)
	if err != nil {
		r.setConditionReady(false, "ClusterRegistrationFailed", err.Error())
	} else {
		r.setConditionReady(true, "ClustersRegistered", "all clusters registered")
	}
	r.aggregatedAPI.Status.URL = r.opts.Server.URL()
	return err
}

func (r *reconciler) registerCluster(ctx context.Context, name string, cluster v1alpha1.AggregatedCluster) error {
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Namespace: r.opts.Namespace,
		Name:      name,
	}
	if err := r.opts.Client.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("getting kubeconfig Secret: %w", err)
	}
	config, err := restConfigFromSecret(secret)
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}

	dynamicClient, err := r.opts.DynamicClient(config)
	if err != nil {
		return fmt.Errorf("building dynamic client: %w", err)
	}
	discovered, err := r.opts.DiscoverResources(config)
	if err != nil {
		return fmt.Errorf("discovering resources: %w", err)
	}

	if err := r.opts.Server.SetCluster(name, dynamicClient, apiserver.Filter(discovered, cluster.APIs)); err != nil {
		return fmt.Errorf("registering on the server: %w", err)
	}
	return nil
}

func (r *reconciler) commitStatus(ctx context.Context) error {
	if r.aggregatedAPI == nil || equality.Semantic.DeepEqual(r.old.Status, r.aggregatedAPI.Status) {
		return nil
	}
	if err := r.opts.Client.Status().Patch(ctx, r.aggregatedAPI, client.MergeFrom(r.old)); err != nil {
		return fmt.Errorf("patching status: %w", err)
	}
	return nil
}
