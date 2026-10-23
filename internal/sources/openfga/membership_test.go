/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import "testing"

func TestParseMembership(t *testing.T) {
	change := func(user, relation, object string) Change {
		var c Change
		c.TupleKey.User, c.TupleKey.Relation, c.TupleKey.Object = user, relation, object
		return c
	}
	role := "role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member"

	got, ok := parseMembership(change("user:alice@example.com", "assignee", role))
	if !ok || got != (membership{account: "team-a", role: "member", email: "alice@example.com"}) {
		t.Errorf("got %+v, %v", got, ok)
	}

	for _, c := range []Change{
		// The second tuple iam-service writes: the role on the account.
		change("role:core_platform-mesh_io_account/32epcrhea5sv5v5b/team-a/member#assignee", "member", "core_platform-mesh_io_account:32epcrhea5sv5v5b/team-a"),
		change("user:*", "assignee", "role:authenticated"),
		change("user:alice@example.com", "assignee", "role:apis_kcp_io_apiexport/abc/export/owner"),
		change("user:alice@example.com", "assignee", "role:core_platform-mesh_io_account/abc/team-a"),
	} {
		if got, ok := parseMembership(c); ok {
			t.Errorf("%+v should not be a membership, got %+v", c.TupleKey, got)
		}
	}
}
