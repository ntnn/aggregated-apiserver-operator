package apiserver

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"sort"
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

func (o *Options) defaults() {
	if o.Hostname == "" {
		o.Hostname = "0.0.0.0"
	}
	if o.Port == 0 {
		o.Port = 6443
	}
}

// RegisterFlags applies defaults and binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {
	o.defaults()
	fs.StringVar(&o.Hostname, "hostname", o.Hostname, "address to bind the aggregated API server to")
	fs.IntVar(&o.Port, "port", o.Port, "port to serve the aggregated API on")
	fs.StringVar(&o.TLSCertFile, "tls-cert-file", o.TLSCertFile, "serving certificate, empty generates a self-signed pair")
	fs.StringVar(&o.TLSKeyFile, "tls-key-file", o.TLSKeyFile, "serving key, empty generates a self-signed pair")
}

// URL returns the endpoint URL the server serves on.
func (o *Options) URL() string {
	return fmt.Sprintf("https://%s:%d", o.Hostname, o.Port)
}

// Server serves an aggregated API whose clusters and resources can change at runtime.
type Server struct {
	opts    Options
	handler atomic.Pointer[http.Handler]

	// mu guards clusters, byCluster and the inner rebuild sequence.
	mu        sync.Mutex
	clusters  map[string]dynamic.Interface
	byCluster map[string][]ServedResource
	resources []ServedResource
}

// New builds a Server; it serves 404 until the first SetCluster.
func New(opts Options) (*Server, error) {
	opts.defaults()

	server := &Server{
		opts:      opts,
		clusters:  map[string]dynamic.Interface{},
		byCluster: map[string][]ServedResource{},
	}
	server.storeHandler(http.NotFoundHandler())
	return server, nil
}

// listen builds the secure serving info, binding the listener.
func (s *Server) listen() (*genericapiserver.SecureServingInfo, error) {
	secureServing := genericoptions.NewSecureServingOptions()
	secureServing.BindAddress = net.ParseIP(s.opts.Hostname)
	secureServing.BindPort = s.opts.Port
	secureServing.ServerCert.CertKey.CertFile = s.opts.TLSCertFile
	secureServing.ServerCert.CertKey.KeyFile = s.opts.TLSKeyFile
	if s.opts.TLSCertFile == "" {
		// self-signed pair is throwaway; keep it out of the working directory
		dir, err := os.MkdirTemp("", "api-aggregator-certs-")
		if err != nil {
			return nil, fmt.Errorf("creating cert directory: %w", err)
		}
		secureServing.ServerCert.CertDirectory = dir
		if err := secureServing.MaybeDefaultWithSelfSignedCerts(s.opts.Hostname, nil, nil); err != nil {
			return nil, fmt.Errorf("generating self-signed serving certs: %w", err)
		}
	}
	var serving *genericapiserver.SecureServingInfo
	if err := secureServing.ApplyTo(&serving); err != nil {
		return nil, fmt.Errorf("applying secure serving options: %w", err)
	}
	return serving, nil
}

func (s *Server) storeHandler(handler http.Handler) {
	s.handler.Store(&handler)
}

// URL returns the endpoint URL the server serves on.
func (s *Server) URL() string {
	return s.opts.URL()
}

// SetCluster adds or updates a member cluster and its served resources.
func (s *Server) SetCluster(name string, client dynamic.Interface, resources []ServedResource) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clusters[name] = client
	s.byCluster[name] = resources
	return s.rebuild()
}

// Clusters returns the names of the registered member clusters.
func (s *Server) Clusters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.clusters))
	for name := range s.clusters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	serving, err := s.listen()
	if err != nil {
		return err
	}
	stopCh := ctx.Done()
	stoppedCh, listenerStoppedCh, err := serving.Serve(s, 10*time.Second, stopCh)
	if err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	<-listenerStoppedCh
	<-stoppedCh
	return nil
}
