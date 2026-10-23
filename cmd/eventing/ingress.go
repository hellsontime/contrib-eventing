/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/platform-mesh/contrib-eventing/internal/ingress"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"
	"github.com/platform-mesh/contrib-eventing/internal/publish"
)

// runIngress accepts events from known publishers and forwards them to the
// broker of their org.
func runIngress(args []string) error {
	fs := flag.NewFlagSet("ingress", flag.ExitOnError)
	addr := fs.String("listen", ":8080", "address to listen on")
	audience := fs.String("audience", "contrib-eventing", "audience publishers' tokens must be issued for")
	publishers := fs.String("publishers", "", "comma-separated users that may publish for any org")
	brokerIngress := fs.String("broker-ingress", "http://broker-ingress.knative-eventing.svc.cluster.local", "Knative broker ingress")
	opts := zap.Options{}
	opts.BindFlags(fs)
	_ = fs.Parse(args)
	log := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(log)

	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	kube, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return err
	}

	pub := &publish.Publisher{Client: &http.Client{Timeout: 10 * time.Second}}
	h := &ingress.Handler{
		Auth:       &ingress.TokenReviewer{Kube: kube, Audience: *audience},
		Publishers: strings.Split(*publishers, ","),
		Kube:       kube,
		Log:        log,
		Forward: func(ctx context.Context, org string, e cloudevents.Event) error {
			return pub.Publish(ctx, fmt.Sprintf("%s/%s/default", *brokerIngress, orgs.Namespace(org)), e)
		},
	}
	srv := &http.Server{Addr: *addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx := ctrl.SetupSignalHandler()
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	log.Info("listening", "addr", *addr, "publishers", *publishers)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
