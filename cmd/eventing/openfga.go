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
	"os"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/platform-mesh/contrib-eventing/internal/keycloak"
	"github.com/platform-mesh/contrib-eventing/internal/publish"
	"github.com/platform-mesh/contrib-eventing/internal/sources/openfga"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// runOpenFGAAdapter reads the OpenFGA changelog of subscribed orgs and sends
// membership events to the ingress.
func runOpenFGAAdapter(args []string) error {
	fs := flag.NewFlagSet("openfga-adapter", flag.ExitOnError)
	fgaURL := fs.String("openfga-url", "http://openfga.platform-mesh-system.svc.cluster.local:8080", "OpenFGA HTTP API")
	keycloakURL := fs.String("keycloak-url", "http://keycloak-service.platform-mesh-system.svc.cluster.local:8080/keycloak", "Keycloak base URL")
	clientID := fs.String("keycloak-client-id", "contrib-eventing", "service account client in the master realm; the secret is read from KEYCLOAK_CLIENT_SECRET")
	ingressURL := fs.String("ingress-url", "http://ingress.contrib-eventing.svc.cluster.local", "where to send events")
	tokenFile := fs.String("token-file", "/var/run/secrets/contrib-eventing/token", "ServiceAccount token the ingress accepts")
	interval := fs.Duration("interval", 5*time.Second, "how often to read the changelog")
	opts := zap.Options{}
	opts.BindFlags(fs)
	_ = fs.Parse(args)
	log := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(log)

	secret := os.Getenv("KEYCLOAK_CLIENT_SECRET")
	namespace := os.Getenv("POD_NAMESPACE")
	if secret == "" || namespace == "" {
		return fmt.Errorf("KEYCLOAK_CLIENT_SECRET and POD_NAMESPACE must be set")
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	kube, err := ctrlruntimeclient.New(cfg, ctrlruntimeclient.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		return err
	}

	ctx := ctrl.SetupSignalHandler()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	pub := &publish.Publisher{Client: httpClient, TokenFile: *tokenFile}
	a := &openfga.Adapter{
		FGA:       &openfga.Client{BaseURL: *fgaURL, HTTP: httpClient},
		Users:     keycloak.NewUsers(ctx, *keycloakURL, *clientID, secret),
		Kube:      kube,
		Namespace: namespace,
		Interval:  *interval,
		Log:       log,
		Publish: func(ctx context.Context, e cloudevents.Event) error {
			return pub.Publish(ctx, *ingressURL, e)
		},
	}
	return a.Run(ctx)
}
