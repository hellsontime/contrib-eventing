/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package contract

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMembershipChanged(t *testing.T) {
	m := Membership{Org: "acme", Account: "team-a", Role: "member", User: User{Email: "alice@example.com"}}
	e, err := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	require.NoError(t, err)
	require.NoError(t, e.Validate())

	assert.Equal(t, TypeMembershipGranted, e.Type())
	assert.Equal(t, "platform-mesh.io/orgs/acme/iam", e.Source())
	assert.Equal(t, "accounts/team-a", e.Subject())
	assert.Equal(t, "acme", e.Extensions()[OrgExtension])
	assert.JSONEq(t, `{"org":"acme","account":"team-a","role":"member","user":{"email":"alice@example.com"}}`, string(e.Data()))

	again, _ := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	other, _ := MembershipChanged(true, Membership{Org: "other"}, "change-1", time.Unix(0, 0))
	assert.Equal(t, e.ID(), again.ID(), "same change, same id")
	assert.NotEqual(t, e.ID(), other.ID(), "ids differ between orgs")

	revoked, _ := MembershipChanged(false, m, "change-2", time.Unix(0, 0))
	assert.Equal(t, TypeMembershipRevoked, revoked.Type())
}
