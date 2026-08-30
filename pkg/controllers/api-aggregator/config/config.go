package config

import (
	"flag"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/apiserver"
	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/api-aggregator/aggregatedapi"
	_ "github.com/ntnn/aggregated-apiserver-operator/pkg/register"
)

// Options configures one api-aggregator instance.
type Options struct {
	// Server is the aggregated API server the controllers drive.
	Server *apiserver.Server

	// AggregatedAPI are the options of the AggregatedAPI controller.
	AggregatedAPI aggregatedapi.Options
}

// RegisterFlags applies defaults and binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {
	o.AggregatedAPI.RegisterFlags(fs)
}

// Setup registers the AggregatedAPI controller with mgr.
func Setup(mgr manager.Manager, opts Options) error {
	controllerOpts := opts.AggregatedAPI
	controllerOpts.Client = mgr.GetClient()
	controllerOpts.Server = opts.Server
	controller, err := aggregatedapi.NewController(controllerOpts)
	if err != nil {
		return fmt.Errorf("building aggregatedapi controller: %w", err)
	}
	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up aggregatedapi controller: %w", err)
	}
	return nil
}
