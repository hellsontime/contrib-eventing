/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	kcpapisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/controller/subscription"
	"github.com/platform-mesh/contrib-eventing/internal/delivery/knative"
)

// runController watches Subscriptions in kcp and manages Knative objects on the
// cluster the controller runs in.
func runController(args []string) error {
	fs := flag.NewFlagSet("subscription-controller", flag.ExitOnError)
	kcpKubeconfig := fs.String("kcp-kubeconfig", "", "kubeconfig for the provider workspace in kcp")
	endpointSlice := fs.String("apiexport-endpointslice", "eventing.platform-mesh.io", "APIExportEndpointSlice to watch through")
	healthAddr := fs.String("health-probe-bind-address", ":8081", "address of the health endpoints")
	opts := zap.Options{}
	opts.BindFlags(fs)
	_ = fs.Parse(args)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	kcpCfg, err := clientcmd.BuildConfigFromFlags("", *kcpKubeconfig)
	if err != nil {
		return fmt.Errorf("kcp kubeconfig: %w", err)
	}
	// The kubeconfig from the platform-mesh-operator already points at the
	// provider workspace; make sure the host has no trailing slash.
	kcpCfg.Host = strings.TrimSuffix(kcpCfg.Host, "/")

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, v1alpha1.AddToScheme, kcpcorev1alpha1.AddToScheme,
		// The apiexport provider reads the APIExportEndpointSlice with this scheme.
		kcpapisv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			return err
		}
	}

	provider, err := apiexport.New(kcpCfg, *endpointSlice, apiexport.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("apiexport provider: %w", err)
	}
	mgr, err := mcmanager.New(kcpCfg, provider, manager.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: *healthAddr,
	})
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return err
	}

	// Knative runs on the local cluster, not in kcp.
	localCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	localClient, err := client.New(localCfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	r := &subscription.Reconciler{Manager: mgr, Delivery: &knative.Backend{Client: localClient}}
	if err := r.SetupWithManager(mgr); err != nil {
		return err
	}
	return mgr.Start(ctrl.SetupSignalHandler())
}
