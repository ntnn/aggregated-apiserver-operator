package aggregatedapi

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

func (r *reconciler) setConditionReady(status bool, reason, message string) {
	metaStatus := metav1.ConditionTrue
	if !status {
		metaStatus = metav1.ConditionFalse
	}

	meta.SetStatusCondition(
		&r.aggregatedAPI.Status.Conditions,
		metav1.Condition{
			Type:               v1alpha1.AggregatedAPIConditionReady,
			ObservedGeneration: r.aggregatedAPI.Generation,
			Status:             metaStatus,
			Reason:             reason,
			Message:            message,
		},
	)
}
