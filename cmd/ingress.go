/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/spf13/cobra"

	"github.com/platform-mesh/contrib-eventing/internal/config"
	"github.com/platform-mesh/contrib-eventing/internal/ingress"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"
	"github.com/platform-mesh/contrib-eventing/internal/publish"
	pmcontext "go.platform-mesh.io/golang-commons/context"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var ingressCfg = config.NewIngressConfig()

var ingressCmd = &cobra.Command{
	Use:   "ingress",
	Short: "check publishers and hand their events to the brokers",
	Run:   runIngress,
}

func init() {
	ingressCfg.AddFlags(ingressCmd.Flags())
}

func runIngress(_ *cobra.Command, _ []string) { // coverage-ignore
	cfg := ingressCfg
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

	pub := &publish.Publisher{Client: &http.Client{Timeout: 10 * time.Second}}
	h := &ingress.Handler{
		Auth:       &ingress.TokenReviewer{Kube: kube, Audience: cfg.Audience},
		Publishers: cfg.Publishers,
		Kube:       kube,
		Log:        log,
		Forward: func(ctx context.Context, org string, e cloudevents.Event) error {
			return pub.Publish(ctx, fmt.Sprintf("%s/%s/default", cfg.BrokerIngress, orgs.Namespace(org)), e)
		},
	}
	srv := &http.Server{Addr: cfg.ListenAddress, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Info().Str("address", cfg.ListenAddress).Strs("publishers", cfg.Publishers).Msg("listening")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal().Err(err).Msg("ingress stopped")
	}
}
