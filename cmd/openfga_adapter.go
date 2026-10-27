/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package cmd

import (
	"context"
	"net/http"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/spf13/cobra"

	"github.com/platform-mesh/contrib-eventing/internal/config"
	"github.com/platform-mesh/contrib-eventing/internal/publish"
	"github.com/platform-mesh/contrib-eventing/internal/sources/openfga"
	pmcontext "go.platform-mesh.io/golang-commons/context"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var openFGAAdapterCfg = config.NewOpenFGAAdapterConfig()

var openFGAAdapterCmd = &cobra.Command{
	Use:   "openfga-adapter",
	Short: "publish membership changes from the OpenFGA changelog",
	Run:   runOpenFGAAdapter,
}

func init() {
	openFGAAdapterCfg.AddFlags(openFGAAdapterCmd.Flags())
}

func runOpenFGAAdapter(_ *cobra.Command, _ []string) { // coverage-ignore
	cfg := openFGAAdapterCfg
	if cfg.Namespace == "" {
		log.Fatal().Msg("POD_NAMESPACE or --namespace must be set")
	}
	if cfg.UserClaim != "email" && cfg.UserClaim != "sub" {
		log.Fatal().Str("claim", cfg.UserClaim).Msg("--user-claim must be email or sub")
	}
	ctx, _, shutdown := pmcontext.StartContext(log, cfg, defaultCfg.ShutdownTimeout)
	defer shutdown()

	restCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("unable to load in-cluster config")
	}
	kube, err := ctrlruntimeclient.New(restCfg, ctrlruntimeclient.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create client")
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	pub := &publish.Publisher{Client: httpClient, TokenFile: cfg.TokenFile}
	a := &openfga.Adapter{
		FGA:        &openfga.Client{BaseURL: cfg.OpenFGAURL, HTTP: httpClient},
		UserClaim:  cfg.UserClaim,
		SkipStores: cfg.SkipStores,
		Kube:       kube,
		Namespace:  cfg.Namespace,
		Interval:   cfg.Interval,
		Log:        log,
		Publish: func(ctx context.Context, e cloudevents.Event) error {
			return pub.Publish(ctx, cfg.IngressURL, e)
		},
	}
	log.Info().Str("openfga", cfg.OpenFGAURL).Str("userClaim", cfg.UserClaim).Msg("starting adapter")
	if err := a.Run(ctx); err != nil {
		log.Fatal().Err(err).Msg("adapter stopped")
	}
}
