/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMembership(t *testing.T) {
	change := func(user, relation, object string) Change {
		var c Change
		c.TupleKey.User, c.TupleKey.Relation, c.TupleKey.Object = user, relation, object
		return c
	}

	got, ok := parseMembership(change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member"))
	assert.True(t, ok)
	assert.Equal(t, membership{account: "team-a", role: "member", user: "alice@example.com"}, got)

	for _, c := range []Change{
		// The second tuple iam-service writes: the role on the account.
		change("role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member#assignee", "member", "core_platform-mesh_io_account:32epcrhea5sv5v5b/team-a"),
		change("user:*", "assignee", "role:authenticated"),
		change("user:alice@example.com", "assignee", "role:apis_kcp_io_apiexport/abc/export/owner"),
		change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account/abc/team-a"),
	} {
		_, ok := parseMembership(c)
		assert.False(t, ok, "%+v is not a membership", c.TupleKey)
	}
}
