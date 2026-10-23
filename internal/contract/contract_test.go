/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package contract

import (
	"testing"
	"time"
)

func TestMembershipChanged(t *testing.T) {
	m := Membership{Org: "acme", Account: "team-a", Role: "member", User: User{ID: "3f2b"}}
	e, err := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("invalid event: %v", err)
	}
	if e.Type() != TypeMembershipGranted || e.Source() != "platform-mesh.io/orgs/acme/iam" ||
		e.Subject() != "accounts/team-a" || e.Extensions()[OrgExtension] != "acme" {
		t.Errorf("unexpected envelope: %s", e)
	}
	if got, want := string(e.Data()), `{"org":"acme","account":"team-a","role":"member","user":{"id":"3f2b"}}`; got != want {
		t.Errorf("data = %s, want %s", got, want)
	}

	again, _ := MembershipChanged(true, m, "change-1", time.Unix(0, 0))
	other, _ := MembershipChanged(true, Membership{Org: "other"}, "change-1", time.Unix(0, 0))
	if again.ID() != e.ID() || other.ID() == e.ID() {
		t.Errorf("ids must be stable per org and change: %s %s %s", e.ID(), again.ID(), other.ID())
	}

	revoked, _ := MembershipChanged(false, m, "change-2", time.Unix(0, 0))
	if revoked.Type() != TypeMembershipRevoked {
		t.Errorf("type = %s", revoked.Type())
	}
}
