package aggregatedapi

import (
	"context"
	"errors"
	"flag"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
)

// ControllerName names the controller in logs and metrics.
const ControllerName = "aggregatedapi"

// Options configures the controller.
type Options struct {
	// AggregatedAPI is the name of the reconciled AggregatedAPI object.
	AggregatedAPI string

	// Client reads AggregatedAPI objects and Secrets from the host cluster.
	Client client.Client

	// Namespace is where the kubeconfig Secrets live.
	Namespace string

	// Server is the aggregated API server clusters are registered on.
	Server *apiserver.Server

	// DiscoveryClient builds a cluster's discovery client.
	// Defaults to [discovery.NewDiscoveryClientForConfig].
	DiscoveryClient func(config *rest.Config) (discovery.DiscoveryInterface, error)

	// DynamicClient builds a cluster's dynamic client.
	// Defaults to [dynamic.NewForConfig].
	DynamicClient func(config *rest.Config) (dynamic.Interface, error)
}

// RegisterFlags applies defaults and binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {
	o.defaults()
	fs.StringVar(&o.AggregatedAPI, "aggregated-api", o.AggregatedAPI, "name of the AggregatedAPI object to serve (required)")
	fs.StringVar(&o.Namespace, "namespace", o.Namespace, "namespace of the kubeconfig Secrets")
}

func (o *Options) defaults() {
	if o.Namespace == "" {
		o.Namespace = "default"
	}
}

func (o *Options) validate() error {
	o.defaults()
	if o.AggregatedAPI == "" {
		return errors.New("an AggregatedAPI name is required")
	}
	if o.Client == nil {
		return errors.New("a Client is required")
	}
	if o.Server == nil {
		return errors.New("a Server is required")
	}
	if o.DiscoveryClient == nil {
		o.DiscoveryClient = func(config *rest.Config) (discovery.DiscoveryInterface, error) {
			return discovery.NewDiscoveryClientForConfig(config)
		}
	}
	if o.DynamicClient == nil {
		o.DynamicClient = func(config *rest.Config) (dynamic.Interface, error) {
			return dynamic.NewForConfig(config)
		}
	}
	return nil
}

// Controller reconciles the AggregatedAPI.
type Controller struct {
	opts Options
}

// NewController validates opts and returns a Controller.
func NewController(opts Options) (*Controller, error) {
	if err := opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid aggregatedapi controller options: %w", err)
	}
	return &Controller{opts: opts}, nil
}

// SetupWithManager wires the controller.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	toAggregatedAPI := handler.EnqueueRequestsFromMapFunc(
		func(ctx context.Context, obj client.Object) []ctrl.Request {
			return []ctrl.Request{
				{
					NamespacedName: client.ObjectKey{
						Name: c.opts.AggregatedAPI,
					},
				},
			}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&v1alpha1.AggregatedAPI{}).
		Watches(&corev1.Secret{}, toAggregatedAPI).
		Complete(c)
}

// Reconcile builds a reconciler for one pass.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Name != c.opts.AggregatedAPI {
		return ctrl.Result{}, nil
	}
	r := &reconciler{
		opts: c.opts,
		log:  ctrllog.FromContext(ctx),
	}
	return r.reconcile(ctx)
}

// restConfigFromSecret parses the "kubeconfig" key of a Secret.
func restConfigFromSecret(secret *corev1.Secret) (*rest.Config, error) {
	raw, ok := secret.Data["kubeconfig"]
	if !ok {
		return nil, errors.New(`missing "kubeconfig" key`)
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	return config, nil
}
