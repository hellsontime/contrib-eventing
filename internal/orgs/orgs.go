/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package orgs maps between organizations and the places their events live.
// Organization workspace, Keycloak realm and OpenFGA store all carry the
// organization name, which is what makes this mapping possible.
package orgs

import "strings"

const (
	// NamespacePrefix is prepended to the organization name to get the namespace
	// on the kind cluster that holds the organization's Knative Broker.
	NamespacePrefix = "pm-events-"
	// Label marks those namespaces with the organization name.
	Label = "eventing.platform-mesh.io/org"
)

// FromPath returns the organization for a kcp workspace path of the form
// root:orgs:<org>. Other paths, including account workspaces below an
// organization, are not organizations.
func FromPath(path string) (string, bool) {
	parts := strings.Split(path, ":")
	if len(parts) != 3 || parts[0] != "root" || parts[1] != "orgs" || parts[2] == "" {
		return "", false
	}
	return parts[2], true
}

// Namespace returns the namespace of an organization's broker.
func Namespace(org string) string {
	return NamespacePrefix + org
}
