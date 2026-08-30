package storage

import (
	"context"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

var _ rest.Watcher = &Storage{}

// Watch establishes a watch on every matching cluster, stamps events
// with cluster metadata and passes it into a consolidated event stream.
func (s *Storage) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	namespace := request.NamespaceValue(ctx)
	if options == nil {
		options = &metainternalversion.ListOptions{}
	}

	remote, matches := splitClusterSelector(options.LabelSelector)

	watchListEnabled := options.SendInitialEvents != nil && *options.SendInitialEvents && options.AllowWatchBookmarks

	selected := make(map[string]bool, len(s.opts.Clusters))
	for cluster := range s.opts.Clusters {
		if matches(cluster) {
			selected[cluster] = true
		}
	}

	watchOptions := metav1.ListOptions{
		FieldSelector:       fieldSelectorString(options),
		AllowWatchBookmarks: false,
		Watch:               true,
	}
	if remote != nil {
		watchOptions.LabelSelector = remote.String()
	}

	watchCtx, cancel := context.WithCancel(ctx)

	// initial events for watch-list
	var initialEvents []watch.Event

	remotes := make(map[string]watch.Interface, len(selected))
	for cluster := range selected {
		client := s.client(s.opts.Clusters[cluster], namespace)
		perCluster := watchOptions
		listOptions := metav1.ListOptions{
			FieldSelector: watchOptions.FieldSelector,
			LabelSelector: watchOptions.LabelSelector,
		}
		if !watchListEnabled {
			// only the resourceVersion is needed to start the watch at "now"
			listOptions.Limit = 1
		}
		list, err := client.List(watchCtx, listOptions)
		if err != nil {
			for _, open := range remotes {
				open.Stop()
			}
			cancel()
			return nil, apierrors.NewInternalError(fmt.Errorf("resolving current resourceVersion of cluster %q: %w", cluster, err))
		}
		if watchListEnabled {
			for i := range list.Items {
				stamp(&list.Items[i], cluster)
				initialEvents = append(initialEvents, watch.Event{Type: watch.Added, Object: &list.Items[i]})
			}
		}
		perCluster.ResourceVersion = list.GetResourceVersion()
		remoteWatch, err := client.Watch(watchCtx, perCluster)
		if err != nil {
			for _, open := range remotes {
				open.Stop()
			}
			cancel()
			return nil, apierrors.NewInternalError(fmt.Errorf("watching cluster %q: %w", cluster, err))
		}
		remotes[cluster] = remoteWatch
	}

	if watchListEnabled {
		bookmark := &unstructured.Unstructured{Object: map[string]any{}}
		bookmark.SetGroupVersionKind(s.opts.Kind)
		bookmark.SetResourceVersion("0") // drop RVs so clients don't falsely rely on a random RV from a random cluster
		bookmark.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
		initialEvents = append(initialEvents, watch.Event{Type: watch.Bookmark, Object: bookmark})
	}

	aggregate := &multiWatch{
		result: make(chan watch.Event),
		cancel: cancel,
	}

	var fanIn sync.WaitGroup
	fanIn.Go(func() {
		for _, event := range initialEvents {
			select {
			case aggregate.result <- event:
			case <-watchCtx.Done():
				return
			}
		}
		var remoteFanIn sync.WaitGroup
		for cluster, remoteWatch := range remotes {
			remoteFanIn.Go(func() {
				aggregate.forward(watchCtx, cluster, remoteWatch)
			})
		}
		remoteFanIn.Wait()
	})

	go func() {
		select {
		case <-watchCtx.Done():
		case <-s.opts.Done:
			cancel()
		}
		for _, remoteWatch := range remotes {
			remoteWatch.Stop()
		}
		fanIn.Wait()
		close(aggregate.result)
	}()

	return aggregate, nil
}

// multiWatch is the aggregate watch.Interface over the remote watches.
type multiWatch struct {
	result chan watch.Event
	cancel context.CancelFunc
	stop   sync.Once
}

// Stop ends the aggregate watch and all remote watches.
func (m *multiWatch) Stop() {
	m.stop.Do(m.cancel)
}

// ResultChan returns the multiplexed event stream.
func (m *multiWatch) ResultChan() <-chan watch.Event {
	return m.result
}

// forward stamps and relays a clusters events until the remote watch ends.
func (m *multiWatch) forward(ctx context.Context, cluster string, remoteWatch watch.Interface) {
	// NOTE(ntnn): this is deliberate - if any stream errors the central cancel
	// function is called, causing all watches to be cancelled.
	// Intent is that when one server has a hiccup the event stream
	// doesn't go stale - the watch is cancelled and the client
	// (hopefully) reestablishes the watch to all servers to have
	// a consistent stream/state across all clusters.
	defer m.cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-remoteWatch.ResultChan():
			if !ok {
				return
			}
			if event.Type == watch.Bookmark {
				// skip bookmarks, they carry RVs of the cluster that
				// sent them and just produce wrong output if clients
				// try to use them  in multi-cluster lists/watches
				continue
			}
			if obj, isUnstructured := event.Object.(*unstructured.Unstructured); isUnstructured {
				stamp(obj, cluster)
			}
			select {
			case m.result <- event:
			case <-ctx.Done():
				return
			}
		}
	}
}
