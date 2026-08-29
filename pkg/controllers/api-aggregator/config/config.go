package config

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"k8s.io/client-go/rest"
)

// Options configures one api-aggregator instance.
type Options struct {
	// AggregatedAPI is the name of the served AggregatedAPI object.
	AggregatedAPI string

	// HostConfig is the rest config for the host cluster.
	HostConfig *rest.Config

	// Hostname is the bind address.
	Hostname string

	// Port is the serving port.
	Port int
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
	fs.StringVar(&o.AggregatedAPI, "aggregated-api", o.AggregatedAPI, "name of the AggregatedAPI object to serve (required)")
	fs.StringVar(&o.Hostname, "hostname", o.Hostname, "address to bind the aggregated API server to")
	fs.IntVar(&o.Port, "port", o.Port, "port to serve the aggregated API on")
}

func (o *Options) validate() error {
	if o.AggregatedAPI == "" {
		return errors.New("AggregatedAPI is required")
	}
	if o.HostConfig == nil {
		return errors.New("HostConfig is required")
	}
	return nil
}

// Run serves the configured AggregatedAPI until ctx is done.
func Run(ctx context.Context, opts Options) error {
	opts.defaults()
	if err := opts.validate(); err != nil {
		return fmt.Errorf("invalid api-aggregator options: %w", err)
	}
	return errors.New("not implemented")
}
