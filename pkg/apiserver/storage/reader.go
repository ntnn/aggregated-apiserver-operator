package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/endpoints/request"

	"golang.org/x/sync/errgroup"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
)

// Get probes all member clusters.
func (s *Storage) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	cluster, obj, err := s.locate(ctx, name, options)
	if err != nil {
		return nil, err
	}
	stamp(obj, cluster)
	return obj, nil
}

// locate finds name in exactly one cluster
func (s *Storage) locate(ctx context.Context, name string, options *metav1.GetOptions) (string, *unstructured.Unstructured, error) {
	namespace := request.NamespaceValue(ctx)
	if options == nil {
		options = &metav1.GetOptions{}
	}

	type hit struct {
		cluster string
		obj     *unstructured.Unstructured
	}
	mu := sync.Mutex{}
	hits := []hit{}
	group, groupCtx := errgroup.WithContext(ctx)
	for cluster, client := range s.opts.Clusters {
		group.Go(func() error {
			obj, err := s.client(client, namespace).Get(groupCtx, name, *options)
			if apierrors.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("getting %s from cluster %q: %w", name, cluster, err)
			}
			mu.Lock()
			hits = append(hits, hit{cluster: cluster, obj: obj})
			mu.Unlock()
			return nil
		})
	}
	// fails closed: unreachable cluster fails the probe, never narrows it
	if err := group.Wait(); err != nil {
		return "", nil, apierrors.NewInternalError(err)
	}

	switch len(hits) {
	case 0:
		return "", nil, apierrors.NewNotFound(s.opts.GVR.GroupResource(), name)
	case 1:
		return hits[0].cluster, hits[0].obj, nil
	default:
		clusters := make([]string, 0, len(hits))
		for _, h := range hits {
			clusters = append(clusters, h.cluster)
		}
		sort.Strings(clusters)
		return "", nil, apierrors.NewConflict(s.opts.GVR.GroupResource(), name,
			fmt.Errorf("found in multiple clusters: %s", strings.Join(clusters, ", ")))
	}
}

// List fans out over the selected clusters and merges the results.
func (s *Storage) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := request.NamespaceValue(ctx)

	remote, matches := splitClusterSelector(options.LabelSelector)

	listOptions := metav1.ListOptions{
		FieldSelector: fieldSelectorString(options),
		Limit:         options.Limit,
	}
	if remote != nil {
		listOptions.LabelSelector = remote.String()
	}

	mu := sync.Mutex{}
	items := []unstructured.Unstructured{}
	group, groupCtx := errgroup.WithContext(ctx)
	for cluster, client := range s.opts.Clusters {
		if !matches(cluster) {
			continue
		}
		group.Go(func() error {
			list, err := s.client(client, namespace).List(groupCtx, listOptions)
			if err != nil {
				return fmt.Errorf("listing from cluster %q: %w", cluster, err)
			}
			for i := range list.Items {
				stamp(&list.Items[i], cluster)
			}
			mu.Lock()
			items = append(items, list.Items...)
			mu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, apierrors.NewInternalError(err)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].GetNamespace() != items[j].GetNamespace() {
			return items[i].GetNamespace() < items[j].GetNamespace()
		}
		if items[i].GetName() != items[j].GetName() {
			return items[i].GetName() < items[j].GetName()
		}
		return items[i].GetAnnotations()[v1alpha1.ClusterAnnotation] < items[j].GetAnnotations()[v1alpha1.ClusterAnnotation]
	})

	list := s.NewList().(*unstructured.UnstructuredList)
	list.Items = items
	return list, nil
}

// ConvertToTable renders NAME/AGE columns for kubectl get.
func (s *Storage) ConvertToTable(ctx context.Context, object, tableOptions runtime.Object) (*metav1.Table, error) {
	return s.table.ConvertToTable(ctx, object, tableOptions)
}

func fieldSelectorString(options *metainternalversion.ListOptions) string {
	if options.FieldSelector == nil {
		return ""
	}
	return options.FieldSelector.String()
}
