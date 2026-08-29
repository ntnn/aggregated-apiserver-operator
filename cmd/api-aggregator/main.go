// Command api-aggregator serves one AggregatedAPI.
package main

import (
	"flag"
	"fmt"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/api-aggregator/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	opts := config.Options{}
	opts.RegisterFlags(flag.CommandLine)
	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	// --kubeconfig is registered by controller-runtime and honoured here.
	hostConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading host-cluster rest config: %w", err)
	}
	opts.HostConfig = hostConfig

	return config.Run(ctrl.SetupSignalHandler(), opts)
}
