package apiserver

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ntnn/aggregated-apiserver-operator/apis/v1alpha1"
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

	// ResyncInterval is how often member discovery is re-resolved to pick
	// up remote API changes.
	ResyncInterval time.Duration
}

func (o *Options) defaults() {
	if o.Hostname == "" {
		o.Hostname = "0.0.0.0"
	}
	if o.Port == 0 {
		o.Port = 6443
	}
	if o.ResyncInterval == 0 {
		o.ResyncInterval = 30 * time.Second
	}
}

// RegisterFlags applies defaults and binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {
	o.defaults()
	fs.StringVar(&o.Hostname, "hostname", o.Hostname, "address to bind the aggregated API server to")
	fs.IntVar(&o.Port, "port", o.Port, "port to serve the aggregated API on")
	fs.StringVar(&o.TLSCertFile, "tls-cert-file", o.TLSCertFile, "serving certificate, empty generates a self-signed pair")
	fs.StringVar(&o.TLSKeyFile, "tls-key-file", o.TLSKeyFile, "serving key, empty generates a self-signed pair")
	fs.DurationVar(&o.ResyncInterval, "resync-interval", o.ResyncInterval, "how often member discovery is re-resolved")
}

// URL returns the endpoint URL the server serves on.
func (o *Options) URL() string {
	return fmt.Sprintf("https://%s:%d", o.Hostname, o.Port)
}

type memberCluster struct {
	client    dynamic.Interface
	discovery discovery.DiscoveryInterface
	selectors []v1alpha1.APISelector
	// resources is the last resolve result.
	resources []ServedResource
}

// resolve discovers and filters the cluster's resources into
// m.resources, reporting whether they changed.
func (m *memberCluster) resolve() (bool, error) {
	_, resourceLists, err := m.discovery.ServerGroupsAndResources()
	if err != nil {
		return false, fmt.Errorf("discovering server resources: %w", err)
	}
	resources, err := FromDiscovery(resourceLists)
	if err != nil {
		return false, fmt.Errorf("reading discovery: %w", err)
	}
	resources = Filter(resources, m.selectors)
	if reflect.DeepEqual(resources, m.resources) {
		return false, nil
	}
	m.resources = resources
	return true, nil
}

// Server serves an aggregated API whose clusters and resources can change at runtime.
type Server struct {
	opts    Options
	handler atomic.Pointer[http.Handler]

	mu          sync.Mutex
	members     map[string]*memberCluster
	resources   []ServedResource
	stopWatches context.CancelFunc
}

// New builds a Server; it serves 404 until the first SetCluster.
func New(opts Options) (*Server, error) {
	opts.defaults()

	server := &Server{
		opts:    opts,
		members: map[string]*memberCluster{},
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

// SetCluster adds or updates a member cluster.
func (s *Server) SetCluster(name string, client dynamic.Interface, discoveryClient discovery.DiscoveryInterface, selectors []v1alpha1.APISelector) error {
	member := &memberCluster{
		client:    client,
		discovery: discoveryClient,
		selectors: selectors,
	}
	if _, err := member.resolve(); err != nil {
		return fmt.Errorf("resolving cluster %q: %w", name, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.members[name] = member
	return s.rebuild()
}

// Clusters returns the names of the registered member clusters.
func (s *Server) Clusters() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.members))
	for name := range s.members {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RemoveCluster removes a member cluster and its served resources.
func (s *Server) RemoveCluster(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.members, name)
	return s.rebuild()
}

// rebuild rebuilds the generic API server.
// Callers hold s.mu.
func (s *Server) rebuild() error {
	byCluster := make(map[string][]ServedResource, len(s.members))
	for name, member := range s.members {
		byCluster[name] = member.resources
	}
	resources, err := Union(byCluster)
	if err != nil {
		return fmt.Errorf("merging served resources: %w", err)
	}

	if reflect.DeepEqual(resources, s.resources) {
		return nil
	}

	if len(resources) == 0 {
		s.swapHandler(http.NotFoundHandler(), nil)
		s.resources = nil
		return nil
	}

	clusters := make(map[string]dynamic.Interface, len(s.members))
	for name, member := range s.members {
		clusters[name] = member.client
	}
	ctx, stopWatches := context.WithCancel(context.Background())
	inner, err := newGenericServer(s.opts.Hostname, s.opts.Port, resources, clusters, ctx.Done())
	if err != nil {
		stopWatches()
		return err
	}

	s.swapHandler(inner.PrepareRun().Handler, stopWatches)
	s.resources = resources
	return nil
}

// swapHandler installs handler and kills the previous watches.
// Callers hold s.mu.
func (s *Server) swapHandler(handler http.Handler, stopWatches context.CancelFunc) {
	s.storeHandler(handler)
	if s.stopWatches != nil {
		s.stopWatches()
	}
	s.stopWatches = stopWatches
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
	go s.resyncLoop(ctx)
	stopCh := ctx.Done()
	stoppedCh, listenerStoppedCh, err := serving.Serve(s, 10*time.Second, stopCh)
	if err != nil {
		return fmt.Errorf("serving: %w", err)
	}
	<-listenerStoppedCh
	<-stoppedCh
	return nil
}

// TODO(ntnn): not happy with a resync to update discovery, but watching
// CRDs is not enough in case the target is also an aggregate.
// Maybe there's some other way to get updates from discovery that
// I don't know.
func (s *Server) resyncLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.ResyncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.resync(ctx)
	}
}

func (s *Server) resync(ctx context.Context) {
	log := ctrllog.FromContext(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for name, member := range s.members {
		memberChanged, err := member.resolve()
		if err != nil {
			// keep serving the last known set; the next tick retries
			log.Error(err, "resolving cluster discovery", "cluster", name)
			continue
		}
		changed = changed || memberChanged
	}
	if !changed {
		return
	}
	if err := s.rebuild(); err != nil {
		log.Error(err, "rebuilding after discovery resync")
	}
}
