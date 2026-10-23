/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Command eventing runs one of the contrib-eventing components. Both live in one
// binary so there is only one image to build and load.
package main

import (
	"fmt"
	"os"
)

const usage = `usage: eventing <component> [flags]

components:
  subscription-controller   turns Subscriptions in kcp into Knative Brokers and Triggers
  keycloak-adapter          publishes Keycloak login events as CloudEvents
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch component, args := os.Args[1], os.Args[2:]; component {
	default:
		_ = args
		fmt.Fprintf(os.Stderr, "unknown component %q\n\n%s", component, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
