/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package contract builds the CloudEvents described in docs/contract.md. Every
// source goes through here, so all events share the same envelope rules.
package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
)

const (
	TypeMembershipGranted = "io.platform-mesh.iam.membership.granted.v1alpha1"
	TypeMembershipRevoked = "io.platform-mesh.iam.membership.revoked.v1alpha1"

	// OrgExtension carries the org name, so subscribers can filter without
	// reading the payload.
	OrgExtension = "pmorg"
)

// User identifies a person the way Platform Mesh does: by the value of the
// user claim the platform admin configured, email by default.
type User struct {
	ID string `json:"id"`
}

type Membership struct {
	Org     string `json:"org"`
	Account string `json:"account"`
	// AccountID is unique in the platform, unlike the name of a nested account.
	AccountID string `json:"accountId"`
	Role      string `json:"role"`
	User      User   `json:"user"`
}

// AccountID is the logical cluster of the workspace that holds the Account
// object and the account's name, the same pair AccountInfo shows as
// originClusterId and name.
func AccountID(cluster, name string) string {
	return cluster + "/" + name
}

// MembershipChanged builds a granted or revoked event. changeKey identifies the
// change at the source; the same change always gives the same event id.
func MembershipChanged(granted bool, m Membership, changeKey string, at time.Time) (cloudevents.Event, error) {
	typ := TypeMembershipRevoked
	if granted {
		typ = TypeMembershipGranted
	}
	e := cloudevents.NewEvent()
	e.SetID(id(m.Org, changeKey))
	e.SetType(typ)
	e.SetSource("platform-mesh.io/orgs/" + m.Org + "/iam")
	e.SetSubject("accounts/" + m.AccountID)
	e.SetTime(at)
	e.SetExtension(OrgExtension, m.Org)
	err := e.SetData(cloudevents.ApplicationJSON, m)
	return e, err
}

func id(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "/")))
	return hex.EncodeToString(sum[:16])
}
