/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package config holds the settings of each component. The settings every
// Platform Mesh component shares (logging, health probes, shutdown) come
// from golang-commons.
package config

import (
	"os"
	"time"

	"github.com/spf13/pflag"
)

type SubscriptionControllerConfig struct {
	KCPKubeconfig          string
	APIExportEndpointSlice string
}

func NewSubscriptionControllerConfig() SubscriptionControllerConfig {
	return SubscriptionControllerConfig{
		KCPKubeconfig:          "/kcp/kubeconfig",
		APIExportEndpointSlice: "eventing.platform-mesh.io",
	}
}

func (c *SubscriptionControllerConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.KCPKubeconfig, "kcp-kubeconfig", c.KCPKubeconfig, "kubeconfig for the provider workspace in kcp")
	fs.StringVar(&c.APIExportEndpointSlice, "apiexport-endpointslice", c.APIExportEndpointSlice, "APIExportEndpointSlice to watch Subscriptions through")
}

type OpenFGAAdapterConfig struct {
	OpenFGAURL       string
	KeycloakURL      string
	KeycloakClientID string
	// KeycloakClientSecret is read from the environment, not from a flag.
	KeycloakClientSecret string
	IngressURL           string
	TokenFile            string
	Interval             time.Duration
	// Namespace holds the adapter's state ConfigMap.
	Namespace string
}

func NewOpenFGAAdapterConfig() OpenFGAAdapterConfig {
	return OpenFGAAdapterConfig{
		OpenFGAURL:           "http://openfga.platform-mesh-system.svc.cluster.local:8080",
		KeycloakURL:          "http://keycloak-service.platform-mesh-system.svc.cluster.local:8080/keycloak",
		KeycloakClientID:     "contrib-eventing",
		KeycloakClientSecret: os.Getenv("KEYCLOAK_CLIENT_SECRET"),
		IngressURL:           "http://ingress.contrib-eventing.svc.cluster.local",
		TokenFile:            "/var/run/secrets/contrib-eventing/token",
		Interval:             5 * time.Second,
		Namespace:            os.Getenv("POD_NAMESPACE"),
	}
}

func (c *OpenFGAAdapterConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.OpenFGAURL, "openfga-url", c.OpenFGAURL, "OpenFGA HTTP API")
	fs.StringVar(&c.KeycloakURL, "keycloak-url", c.KeycloakURL, "Keycloak base URL")
	fs.StringVar(&c.KeycloakClientID, "keycloak-client-id", c.KeycloakClientID, "service account client in the Keycloak master realm; the secret comes from KEYCLOAK_CLIENT_SECRET")
	fs.StringVar(&c.IngressURL, "ingress-url", c.IngressURL, "where to send events")
	fs.StringVar(&c.TokenFile, "token-file", c.TokenFile, "ServiceAccount token the ingress accepts")
	fs.DurationVar(&c.Interval, "interval", c.Interval, "how often to read the changelog")
	fs.StringVar(&c.Namespace, "namespace", c.Namespace, "namespace of the state ConfigMap (default from POD_NAMESPACE)")
}

type IngressConfig struct {
	ListenAddress string
	Audience      string
	// Publishers may send events for any org.
	Publishers    []string
	BrokerIngress string
}

func NewIngressConfig() IngressConfig {
	return IngressConfig{
		ListenAddress: ":8080",
		Audience:      "contrib-eventing",
		BrokerIngress: "http://broker-ingress.knative-eventing.svc.cluster.local",
	}
}

func (c *IngressConfig) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.ListenAddress, "listen-address", c.ListenAddress, "address to accept events on")
	fs.StringVar(&c.Audience, "audience", c.Audience, "audience publishers' tokens must be issued for")
	fs.StringSliceVar(&c.Publishers, "publishers", c.Publishers, "users that may publish for any org")
	fs.StringVar(&c.BrokerIngress, "broker-ingress", c.BrokerIngress, "Knative broker ingress")
}
