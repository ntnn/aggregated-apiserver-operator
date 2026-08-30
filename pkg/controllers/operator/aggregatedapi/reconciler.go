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

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

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

	if err := r.opts.Apply(ctx, r.deployment()); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying Deployment: %w", err)
	}
	if err := r.opts.Apply(ctx, r.service()); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying Service: %w", err)
	}
	return ctrl.Result{}, nil
}

// childName is the name of the child Deployment and Service.
func (r *reconciler) childName() string {
	return "aggregator-" + r.aggregatedAPI.Name
}

func (r *reconciler) labels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "api-aggregator",
		"app.kubernetes.io/instance":   r.aggregatedAPI.Name,
		"app.kubernetes.io/managed-by": FieldOwner,
	}
}

func (r *reconciler) ownerReference() metav1.OwnerReference {
	return *metav1.NewControllerRef(
		r.aggregatedAPI,
		v1alpha1.GroupVersion.WithKind("AggregatedAPI"),
	)
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
			Name:            r.childName(),
			Namespace:       r.aggregatedAPI.Namespace,
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{r.ownerReference()},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
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
			Namespace: r.aggregatedAPI.Namespace,
			Labels:    r.labels(),
			OwnerReferences: []metav1.OwnerReference{
				r.ownerReference(),
			},
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
