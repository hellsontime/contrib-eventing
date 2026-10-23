/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package orgs

import "testing"

func TestFromPath(t *testing.T) {
	for path, want := range map[string]string{
		"root:orgs:acme":        "acme",
		"root:orgs:acme:team-a": "",
		"root:orgs":             "",
		"root:providers:acme":   "",
		"root:orgs:":            "",
	} {
		got, ok := FromPath(path)
		if got != want || ok != (want != "") {
			t.Errorf("FromPath(%q) = %q, %v; want %q", path, got, ok, want)
		}
	}
}
