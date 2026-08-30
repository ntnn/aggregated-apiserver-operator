package apiserver

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/dynamic"
)

// Options configures a Server.
type Options struct {
	// Hostname is the bind address.
	Hostname string

	// Port is the serving port.
	Port int

	// TLSCertFile and TLSKeyFile are the serving certificate; empty self-signs.
	TLSCertFile string
	TLSKeyFile  string
}

func (o *Options) validate() error {
	if o.Hostname == "" {
		return errors.New("hostname is required")
	}
	if o.Port == 0 {
		return errors.New("port is required")
	}
	return nil
}

// Server serves an aggregated API whose clusters and resources can change at runtime.
type Server struct {
	opts    Options
	serving *genericapiserver.SecureServingInfo
	handler atomic.Pointer[http.Handler]

	// mu guards clusters, byCluster and the inner rebuild sequence.
	mu        sync.Mutex
	clusters  map[string]dynamic.Interface
	byCluster map[string][]ServedResource
	resources []ServedResource
}

// New builds a Server.
func New(opts Options) (*Server, error) {
	if err := opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid apiserver options: %w", err)
	}

	secureServing := genericoptions.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP(opts.Hostname)
	secureServing.BindPort = opts.Port
	secureServing.ServerCert.CertKey.CertFile = opts.TLSCertFile
	secureServing.ServerCert.CertKey.KeyFile = opts.TLSKeyFile
	if opts.TLSCertFile == "" {
		if err := secureServing.MaybeDefaultWithSelfSignedCerts(opts.Hostname, nil, nil); err != nil {
			return nil, fmt.Errorf("generating self-signed serving certs: %w", err)
		}
	}
	var serving *genericapiserver.SecureServingInfo
	if err := secureServing.ApplyTo(&serving); err != nil {
		return nil, fmt.Errorf("applying secure serving options: %w", err)
	}

	server := &Server{
		opts:      opts,
		serving:   serving,
		clusters:  map[string]dynamic.Interface{},
		byCluster: map[string][]ServedResource{},
	}
	server.storeHandler(http.NotFoundHandler())
	return server, nil
}

func (s *Server) storeHandler(handler http.Handler) {
	s.handler.Store(&handler)
}

// SetCluster adds or updates a member cluster and its served resources.
func (s *Server) SetCluster(name string, client dynamic.Interface, resources []ServedResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusters[name] = client
	s.byCluster[name] = resources
	return s.rebuild()
}

// RemoveCluster removes a member cluster and its served resources.
func (s *Server) RemoveCluster(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.clusters, name)
	delete(s.byCluster, name)
	return s.rebuild()
}

// rebuild rebuilds the generic API server.
// Callers hold s.mu.
func (s *Server) rebuild() error {
	resources, err := Union(s.byCluster)
	if err != nil {
		return fmt.Errorf("merging served resources: %w", err)
	}

	if len(resources) == 0 {
		s.storeHandler(http.NotFoundHandler())
		s.resources = nil
		return nil
	}

	clusters := maps.Clone(s.clusters)
	inner, err := newGenericServer(s.opts.Hostname, s.opts.Port, resources, clusters)
	if err != nil {
		return err
	}

	s.storeHandler(inner.PrepareRun().Handler)
	s.resources = resources
	return nil
}

// ServeHTTP delegates to the current inner handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	(*s.handler.Load()).ServeHTTP(w, r)
}

// Run serves until ctx is done.
func (s *Server) Run(ctx context.Context) error {
	stopCh := ctx.Done()
	stoppedCh, listenerStoppedCh, err := s.serving.Serve(s, 10*time.Second, stopCh)
	if err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	<-listenerStoppedCh
	<-stoppedCh
	return nil
}
