/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
)

type fakeUsers map[string]string

func (f fakeUsers) IDByEmail(_ context.Context, _, email string) (string, error) { return f[email], nil }

func TestPublishNew(t *testing.T) {
	changes := `{"changes": [
	  {"operation": "TUPLE_OPERATION_WRITE", "timestamp": "2026-10-23T13:43:24Z",
	   "tuple_key": {"user": "user:alice@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c1/team-a/member"}},
	  {"operation": "TUPLE_OPERATION_DELETE", "timestamp": "2026-10-23T13:43:25Z",
	   "tuple_key": {"user": "user:new@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c1/team-a/owner"}}
	], "continuation_token": "after-page"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("continuation_token") == "after-page" {
			_, _ = w.Write([]byte(`{"changes": [], "continuation_token": "after-page"}`))
			return
		}
		_, _ = w.Write([]byte(changes))
	}))
	defer srv.Close()

	var published []cloudevents.Event
	fail := true
	a := &Adapter{
		FGA:      &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Users:    fakeUsers{"alice@example.com": "alice-id"},
		Log:      logr.Discard(),
		storeIDs: map[string]string{"acme": "store-1"},
		Publish: func(_ context.Context, e cloudevents.Event) error {
			if fail && len(published) == 1 {
				return errors.New("ingress down")
			}
			published = append(published, e)
			return nil
		},
	}

	st := orgState{Since: time.Now()}
	if moved, err := a.publishNew(context.Background(), "acme", &st); err == nil || moved || st.Token != "" {
		t.Fatalf("a failed page must not move the token: moved=%v err=%v state=%+v", moved, err, st)
	}

	fail, published = false, nil
	if moved, err := a.publishNew(context.Background(), "acme", &st); err != nil || !moved || st.Token != "after-page" {
		t.Fatalf("moved=%v err=%v state=%+v", moved, err, st)
	}
	if len(published) != 2 {
		t.Fatalf("published %d events", len(published))
	}

	var granted, revoked contract.Membership
	_ = json.Unmarshal(published[0].Data(), &granted)
	_ = json.Unmarshal(published[1].Data(), &revoked)
	if published[0].Type() != contract.TypeMembershipGranted || granted.User != (contract.User{ID: "alice-id"}) {
		t.Errorf("granted: %s %+v", published[0].Type(), granted)
	}
	// new@example.com has no Keycloak user yet, so the event carries the email.
	if published[1].Type() != contract.TypeMembershipRevoked || revoked.User != (contract.User{Email: "new@example.com"}) || revoked.Role != "owner" {
		t.Errorf("revoked: %s %+v", published[1].Type(), revoked)
	}
}
