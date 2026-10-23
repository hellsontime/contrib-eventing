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
	"strings"
	"testing"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
)

type fakeUsers map[string]string

func (f fakeUsers) IDByEmail(_ context.Context, _, email string) (string, error) {
	return f[email], nil
}

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

	st := orgState{StoreID: "store-1", Since: time.Now()}
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

// An org that was deleted and created again has a new store. The old token
// must not be used with it.
func TestPublishNewWithRecreatedStore(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		switch r.URL.Path {
		case "/stores":
			_, _ = w.Write([]byte(`{"stores": [{"id": "new-store", "name": "acme"}]}`))
		case "/stores/old-store/changes":
			http.Error(w, `{"code": "store_id_not_found"}`, http.StatusNotFound)
		default:
			_, _ = w.Write([]byte(`{"changes": []}`))
		}
	}))
	defer srv.Close()

	a := &Adapter{
		FGA:      &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Log:      logr.Discard(),
		storeIDs: map[string]string{"acme": "old-store"},
	}
	st := orgState{StoreID: "old-store", Token: "old-token", Since: time.Now().Add(-time.Hour)}

	if _, err := a.publishNew(context.Background(), "acme", &st); err == nil {
		t.Fatal("expected an error for the deleted store")
	}
	if moved, err := a.publishNew(context.Background(), "acme", &st); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	if st.StoreID != "new-store" || st.Token != "" || time.Since(st.Since) > time.Minute {
		t.Errorf("state should start fresh on the new store: %+v", st)
	}
	last := requests[len(requests)-1]
	if !strings.HasPrefix(last, "/stores/new-store/changes?") || strings.Contains(last, "old-token") {
		t.Errorf("last request %s", last)
	}
}
