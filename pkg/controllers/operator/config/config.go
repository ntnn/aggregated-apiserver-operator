package config

import (
	"flag"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/operator/aggregatedapi"
)

// Options configures the operator.
type Options struct {
	AggregatedAPI aggregatedapi.Options
}

// RegisterFlags applies defaults and binds the flag-settable options to fs.
func (o *Options) RegisterFlags(fs *flag.FlagSet) {
	o.AggregatedAPI.RegisterFlags(fs)
}

// Setup registers the operator's controllers with mgr.
func Setup(mgr manager.Manager, opts Options) error {
	controller, err := aggregatedapi.NewController(opts.AggregatedAPI)
	if err != nil {
		return fmt.Errorf("building aggregatedapi controller: %w", err)
	}
	if err := controller.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("setting up aggregatedapi controller: %w", err)
	}
	return nil
}
