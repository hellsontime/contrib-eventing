/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package orgs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromPath(t *testing.T) {
	for path, want := range map[string]string{
		"root:orgs:acme":        "acme",
		"root:orgs:acme:team-a": "",
		"root:orgs":             "",
		"root:providers:acme":   "",
		"root:orgs:":            "",
	} {
		got, ok := FromPath(path)
		assert.Equal(t, want, got, path)
		assert.Equal(t, want != "", ok, path)
	}
}
