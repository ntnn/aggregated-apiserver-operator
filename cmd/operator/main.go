// Command operator manages api-aggregator deployments.
package main

import (
	"flag"
	"fmt"
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/ntnn/aggregated-apiserver-operator/pkg/controllers/operator/config"
	_ "github.com/ntnn/aggregated-apiserver-operator/pkg/register"
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

	hostConfig, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("loading rest config: %w", err)
	}

	mgr, err := ctrl.NewManager(hostConfig, manager.Options{
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		return fmt.Errorf("building manager: %w", err)
	}

	if err := config.Setup(mgr, opts); err != nil {
		return fmt.Errorf("setting up controllers: %w", err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("running manager: %w", err)
	}
	return nil
}
