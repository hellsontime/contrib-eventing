/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/platform-mesh/contrib-eventing/internal/config"
	"github.com/platform-mesh/contrib-eventing/internal/kcpsetup"

	"k8s.io/client-go/tools/clientcmd"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var kcpSetupCfg = config.NewKCPSetupConfig()

var kcpSetupCmd = &cobra.Command{
	Use:   "kcp-setup",
	Short: "put the eventing API into the provider workspace in kcp",
	Run:   runKCPSetup,
}

func init() {
	kcpSetupCfg.AddFlags(kcpSetupCmd.Flags())
}

func runKCPSetup(_ *cobra.Command, _ []string) { // coverage-ignore
	cfg := kcpSetupCfg
	restCfg, err := clientcmd.BuildConfigFromFlags("", cfg.KCPKubeconfig)
	if err != nil {
		log.Fatal().Err(err).Msg("unable to load kcp kubeconfig")
	}
	c, err := ctrlruntimeclient.New(restCfg, ctrlruntimeclient.Options{})
	if err != nil {
		log.Fatal().Err(err).Msg("unable to create kcp client")
	}
	objs, err := kcpsetup.Objects()
	if err != nil {
		log.Fatal().Err(err).Msg("broken kcp manifests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	log.Info().Str("host", restCfg.Host).Msg("applying the eventing API")
	if err := kcpsetup.Apply(ctx, c, objs, 2*time.Second); err != nil {
		log.Fatal().Err(err).Msg("kcp setup failed")
	}
	log.Info().Msg("eventing API ready")
}
