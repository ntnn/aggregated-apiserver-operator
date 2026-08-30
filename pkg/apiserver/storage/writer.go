package storage

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	"golang.org/x/sync/errgroup"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

var (
	_ rest.Creater           = &Storage{}
	_ rest.Updater           = &Storage{}
	_ rest.GracefulDeleter   = &Storage{}
	_ rest.CollectionDeleter = &Storage{}
)

// Create routes to the cluster named by the cluster annotation.
// If only one cluster serves the API the annotation is optional.
func (s *Storage) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, apierrors.NewInternalError(fmt.Errorf("expected unstructured object, got %T", obj))
	}

	cluster := u.GetAnnotations()[v1alpha1.ClusterAnnotation]
	if cluster == "" {
		if len(s.opts.Clusters) != 1 {
			return nil, apierrors.NewBadRequest(fmt.Sprintf("create requires the %s annotation naming the target cluster", v1alpha1.ClusterAnnotation))
		}
		// unambiguous: the single serving cluster is the implicit target
		for name := range s.opts.Clusters {
			cluster = name
		}
	}
	client, ok := s.opts.Clusters[cluster]
	if !ok {
		return nil, apierrors.NewBadRequest(fmt.Sprintf("cluster %q is not part of this aggregated API", cluster))
	}

	if createValidation != nil {
		if err := createValidation(ctx, u); err != nil {
			return nil, err
		}
	}

	u = u.DeepCopy()
	strip(u)
	created, err := s.client(client, request.NamespaceValue(ctx)).Create(ctx, u, *options)
	if err != nil {
		return nil, err
	}
	stamp(created, cluster)
	return created, nil
}

// Update routes to the cluster owning the existing object.
func (s *Storage) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	cluster, existing, err := s.locate(ctx, name, nil)
	if apierrors.IsNotFound(err) && forceAllowCreate {
		// the objects exists in no remote and forceAllowCreate/SSA is used
		// pass the object to .Create
		base := s.New()
		obj, updatedErr := objInfo.UpdatedObject(ctx, base)
		if updatedErr != nil {
			return nil, false, updatedErr
		}
		created, createErr := s.Create(ctx, obj, createValidation, &metav1.CreateOptions{
			DryRun:          options.DryRun,
			FieldManager:    options.FieldManager,
			FieldValidation: options.FieldValidation,
		})
		return created, createErr == nil, createErr
	}
	if err != nil {
		return nil, false, err
	}
	// updates are computed against the stamped object clients read
	stamp(existing, cluster)

	obj, err := objInfo.UpdatedObject(ctx, existing)
	if err != nil {
		return nil, false, err
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, false, apierrors.NewInternalError(fmt.Errorf("expected unstructured object, got %T", obj))
	}

	if updateValidation != nil {
		if err := updateValidation(ctx, u, existing); err != nil {
			return nil, false, err
		}
	}

	u = u.DeepCopy()
	strip(u)
	updated, err := s.client(s.opts.Clusters[cluster], request.NamespaceValue(ctx)).Update(ctx, u, *options)
	if err != nil {
		return nil, false, err
	}
	stamp(updated, cluster)
	return updated, false, nil
}

// Delete routes to the cluster owning the object; ambiguous names conflict.
func (s *Storage) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	cluster, existing, err := s.locate(ctx, name, nil)
	if err != nil {
		return nil, false, err
	}

	if deleteValidation != nil {
		if err := deleteValidation(ctx, existing); err != nil {
			return nil, false, err
		}
	}

	if err := s.client(s.opts.Clusters[cluster], request.NamespaceValue(ctx)).Delete(ctx, name, *options); err != nil {
		return nil, false, err
	}
	stamp(existing, cluster)
	return existing, true, nil
}

// DeleteCollection fans out over the clusters selected by the virtual cluster label.
func (s *Storage) DeleteCollection(ctx context.Context, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions, listOptions *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := request.NamespaceValue(ctx)
	if listOptions == nil {
		listOptions = &metainternalversion.ListOptions{}
	}
	remote, matches := splitClusterSelector(listOptions.LabelSelector)

	remoteListOptions := metav1.ListOptions{
		FieldSelector: fieldSelectorString(listOptions),
	}
	if remote != nil {
		remoteListOptions.LabelSelector = remote.String()
	}

	mu := sync.Mutex{}
	deleted := []unstructured.Unstructured{}
	group, groupCtx := errgroup.WithContext(ctx)
	for cluster, client := range s.opts.Clusters {
		if !matches(cluster) {
			continue
		}
		group.Go(func() error {
			resource := s.client(client, namespace)
			// list first to report what was deleted, stamped per cluster
			list, err := resource.List(groupCtx, remoteListOptions)
			if err != nil {
				return fmt.Errorf("listing from cluster %q: %w", cluster, err)
			}
			if err := resource.DeleteCollection(groupCtx, *options, remoteListOptions); err != nil {
				return fmt.Errorf("deleting collection in cluster %q: %w", cluster, err)
			}
			for i := range list.Items {
				stamp(&list.Items[i], cluster)
			}
			mu.Lock()
			deleted = append(deleted, list.Items...)
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	list := s.NewList().(*unstructured.UnstructuredList)
	list.Items = deleted
	return list, nil
}
