/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package provider holds the kcp manifests of the eventing API. The kcp-setup
// command applies them to the provider workspace.
package provider

import "embed"

//go:embed *.yaml
var Manifests embed.FS
