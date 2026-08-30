package storage

import (
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/dynamic"
)

// Options configures a Storage for one GVR.
type Options struct {
	// GVR is the resource served by this storage.
	GVR schema.GroupVersionResource

	// Kind is the kind of the served objects.
	Kind schema.GroupVersionKind

	// Namespaced reports whether the resource is namespace-scoped.
	Namespaced bool

	// Singular is the singular resource name; may be empty.
	Singular string

	// Clusters maps member cluster names to their dynamic clients.
	Clusters map[string]dynamic.Interface

	// Done ends all watches served by this storage when closed;
	// membership changes swap the inner server and close it.
	Done <-chan struct{}
}

func (o *Options) validate() error {
	if o.GVR.Resource == "" {
		return errors.New("GVR is required")
	}
	if o.Kind.Kind == "" {
		return errors.New("Kind is required")
	}
	if len(o.Clusters) == 0 {
		return errors.New("at least one cluster is required")
	}
	return nil
}

// Storage is a proxy rest.Storage serving one GVR from member clusters.
type Storage struct {
	opts  Options
	table rest.TableConvertor
}

var (
	_ rest.Storage              = &Storage{}
	_ rest.Scoper               = &Storage{}
	_ rest.KindProvider         = &Storage{}
	_ rest.SingularNameProvider = &Storage{}
	_ rest.Getter               = &Storage{}
	_ rest.Lister               = &Storage{}
)

// New returns a Storage serving opts.GVR.
func New(opts Options) (*Storage, error) {
	if err := opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid storage options: %w", err)
	}
	return &Storage{
		opts:  opts,
		table: rest.NewDefaultTableConvertor(opts.GVR.GroupResource()),
	}, nil
}

// New returns an empty object of the served kind.
func (s *Storage) New() runtime.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(s.opts.Kind)
	return obj
}

// NewList returns an empty list of the served kind.
func (s *Storage) NewList() runtime.Object {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(s.opts.Kind.GroupVersion().WithKind(s.opts.Kind.Kind + "List"))
	return list
}

// Destroy implements rest.Storage; there is nothing to release.
func (s *Storage) Destroy() {}

// NamespaceScoped reports whether the resource is namespace-scoped.
func (s *Storage) NamespaceScoped() bool {
	return s.opts.Namespaced
}

// Kind returns the served kind.
func (s *Storage) Kind() string {
	return s.opts.Kind.Kind
}

// GetSingularName returns the singular resource name.
func (s *Storage) GetSingularName() string {
	return s.opts.Singular
}

// client returns the resource client for cluster, namespace-scoped when applicable.
func (s *Storage) client(cluster dynamic.Interface, namespace string) dynamic.ResourceInterface {
	if s.opts.Namespaced && namespace != "" {
		return cluster.Resource(s.opts.GVR).Namespace(namespace)
	}
	return cluster.Resource(s.opts.GVR)
}
