package aggregatedapi

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// Finalizer marks AggregatedAPIs with child workloads to clean up.
const Finalizer = "aggregation.ntnn.dev/child-workloads"

// reconciler holds the state of a single reconcile pass.
type reconciler struct {
	opts Options
	req  ctrl.Request
	log  logr.Logger

	aggregatedAPI *v1alpha1.AggregatedAPI
}

func (r *reconciler) reconcile(ctx context.Context) (ctrl.Result, error) {
	aggregatedAPI, err := r.opts.GetAggregatedAPI(ctx, r.req.NamespacedName)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting AggregatedAPI: %w", err)
	}
	r.aggregatedAPI = aggregatedAPI

	if !aggregatedAPI.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx)
	}

	if controllerutil.AddFinalizer(aggregatedAPI, Finalizer) {
		if err := r.opts.Update(ctx, aggregatedAPI); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
		}
	}

	if err := r.opts.Apply(ctx, r.deployment()); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying Deployment: %w", err)
	}
	if err := r.opts.Apply(ctx, r.service()); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying Service: %w", err)
	}
	return ctrl.Result{}, nil
}

// finalize deletes the child workloads and releases the finalizer.
func (r *reconciler) finalize(ctx context.Context) error {
	if !controllerutil.ContainsFinalizer(r.aggregatedAPI, Finalizer) {
		return nil
	}
	if err := r.opts.Delete(ctx, r.deployment()); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Deployment: %w", err)
	}
	if err := r.opts.Delete(ctx, r.service()); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting Service: %w", err)
	}
	controllerutil.RemoveFinalizer(r.aggregatedAPI, Finalizer)
	if err := r.opts.Update(ctx, r.aggregatedAPI); err != nil {
		return fmt.Errorf("removing finalizer: %w", err)
	}
	return nil
}

// childName is the name of the child Deployment and Service. It carries
// the AggregatedAPI's namespace: all children share one namespace.
func (r *reconciler) childName() string {
	return "aggregator-" + r.aggregatedAPI.Namespace + "-" + r.aggregatedAPI.Name
}

func (r *reconciler) labels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "api-aggregator",
		"app.kubernetes.io/instance":   r.aggregatedAPI.Namespace + "." + r.aggregatedAPI.Name,
		"app.kubernetes.io/managed-by": FieldOwner,
	}
}

func (r *reconciler) image() string {
	if r.aggregatedAPI.Spec.Image != "" {
		return r.aggregatedAPI.Spec.Image
	}
	return v1alpha1.DefaultImage
}

func (r *reconciler) deployment() *appsv1.Deployment {
	labels := r.labels()
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: appsv1.SchemeGroupVersion.String(),
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.childName(),
			Namespace: r.opts.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: r.opts.ServiceAccount,
					Containers: []corev1.Container{
						{
							Name:  "api-aggregator",
							Image: r.image(),
							Args: []string{
								"--aggregated-api", r.aggregatedAPI.Name,
								"--namespace", r.aggregatedAPI.Namespace,
							},
							Ports: []corev1.ContainerPort{
								{Name: "https", ContainerPort: 6443},
							},
						},
					},
				},
			},
		},
	}
}

func (r *reconciler) service() *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.childName(),
			Namespace: r.opts.Namespace,
			Labels:    r.labels(),
		},
		Spec: corev1.ServiceSpec{
			Selector: r.labels(),
			Ports: []corev1.ServicePort{
				{
					Name:       "https",
					Port:       443,
					TargetPort: intstr.FromString("https"),
				},
			},
		},
	}
}
