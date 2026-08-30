package aggregatedapi

import (
	"context"
	"flag"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// ControllerName names the controller in logs and metrics.
const ControllerName = "operator-aggregatedapi"

// FieldOwner is the server-side apply field manager of this controller.
const FieldOwner = "aggregatedapi-operator"

// Options configures the controller.
type Options struct {
	// GetAggregatedAPI fetches the reconciled object.
	// Defaults to the manager's client in SetupWithManager.
	GetAggregatedAPI func(ctx context.Context, key client.ObjectKey) (*v1alpha1.AggregatedAPI, error)

	// Apply server-side applies a desired child object.
	// Defaults to the manager's client in SetupWithManager.
	Apply func(ctx context.Context, obj client.Object) error
}

// RegisterFlags binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {}

func (o *Options) validate() error {
	return nil
}

// Controller reconciles AggregatedAPI objects into child workloads.
type Controller struct {
	opts Options
}

// NewController validates opts and returns a Controller.
func NewController(opts Options) (*Controller, error) {
	if err := opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid operator aggregatedapi controller options: %w", err)
	}
	return &Controller{opts: opts}, nil
}

// +kubebuilder:rbac:groups=aggregation.ntnn.dev,resources=aggregatedapis,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;patch

// SetupWithManager wires the controller and defaults the client seams.
func (c *Controller) SetupWithManager(mgr ctrl.Manager) error {
	if c.opts.GetAggregatedAPI == nil {
		c.opts.GetAggregatedAPI = func(ctx context.Context, key client.ObjectKey) (*v1alpha1.AggregatedAPI, error) {
			aggregatedAPI := &v1alpha1.AggregatedAPI{}
			if err := mgr.GetClient().Get(ctx, key, aggregatedAPI); err != nil {
				return nil, err //nolint:wrapcheck // callers match apierrors
			}
			return aggregatedAPI, nil
		}
	}
	if c.opts.Apply == nil {
		c.opts.Apply = func(ctx context.Context, obj client.Object) error {
			// Client.Apply wants an ApplyConfiguration; wrap the typed object
			content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			if err != nil {
				return fmt.Errorf("converting %T to unstructured: %w", obj, err)
			}
			apply := client.ApplyConfigurationFromUnstructured(&unstructured.Unstructured{Object: content})
			return mgr.GetClient().Apply(ctx, apply, client.FieldOwner(FieldOwner), client.ForceOwnership)
		}
	}

	//nolint:wrapcheck // setup error passes through
	return ctrl.NewControllerManagedBy(mgr).
		Named(ControllerName).
		For(&v1alpha1.AggregatedAPI{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(c)
}

// Reconcile builds a reconciler for one pass.
func (c *Controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r := &reconciler{
		opts: c.opts,
		req:  req,
		log:  ctrllog.FromContext(ctx),
	}
	return r.reconcile(ctx)
}
