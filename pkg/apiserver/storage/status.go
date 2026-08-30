package storage

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type StatusStorage struct {
	parent *Storage
}

var (
	_ rest.Storage = &StatusStorage{}
	_ rest.Getter  = &StatusStorage{}
	_ rest.Updater = &StatusStorage{}
)

// NewStatus returns the status subresource storage for parent.
func NewStatus(parent *Storage) *StatusStorage {
	return &StatusStorage{parent: parent}
}

// New returns an empty object of the served kind.
func (s *StatusStorage) New() runtime.Object {
	return s.parent.New()
}

// Destroy implements rest.Storage.
func (s *StatusStorage) Destroy() {}

// Get returns the parent object.
func (s *StatusStorage) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	return s.parent.Get(ctx, name, options)
}

// Update routes to the cluster owning the existing object.
func (s *StatusStorage) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	cluster, existing, err := s.parent.locate(ctx, name, nil, s.parent.bodyCluster(ctx, objInfo))
	if err != nil {
		return nil, false, err
	}
	stamp(existing, cluster)

	obj, err := objInfo.UpdatedObject(ctx, existing)
	if err != nil {
		return nil, false, err
	}
	u, convErr := s.parent.asUnstructured(obj)
	if convErr != nil {
		return nil, false, convErr
	}

	if updateValidation != nil {
		if err := updateValidation(ctx, u, existing); err != nil {
			return nil, false, err
		}
	}

	u = u.DeepCopy()
	strip(u)
	client := s.parent.client(s.parent.opts.Clusters[cluster], request.NamespaceValue(ctx))
	updated, err := client.UpdateStatus(ctx, u, *options)
	if err != nil {
		return nil, false, err
	}
	stamp(updated, cluster)
	return updated, false, nil
}
