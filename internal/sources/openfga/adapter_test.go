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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"go.platform-mesh.io/golang-commons/logger/testlogger"
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
		Log:      testlogger.New().HideLogOutput().Logger,
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
	moved, err := a.publishNew(context.Background(), "acme", &st)
	require.Error(t, err)
	assert.False(t, moved, "a failed page must not move the token")
	assert.Empty(t, st.Token)

	fail, published = false, nil
	moved, err = a.publishNew(context.Background(), "acme", &st)
	require.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, "after-page", st.Token)
	require.Len(t, published, 2)

	var granted, revoked contract.Membership
	require.NoError(t, json.Unmarshal(published[0].Data(), &granted))
	require.NoError(t, json.Unmarshal(published[1].Data(), &revoked))
	assert.Equal(t, contract.TypeMembershipGranted, published[0].Type())
	assert.Equal(t, contract.User{ID: "alice-id"}, granted.User)
	// new@example.com has no Keycloak user yet, so the event carries the email.
	assert.Equal(t, contract.TypeMembershipRevoked, published[1].Type())
	assert.Equal(t, contract.User{Email: "new@example.com"}, revoked.User)
	assert.Equal(t, "owner", revoked.Role)
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
		Log:      testlogger.New().HideLogOutput().Logger,
		storeIDs: map[string]string{"acme": "old-store"},
	}
	st := orgState{StoreID: "old-store", Token: "old-token", Since: time.Now().Add(-time.Hour)}

	_, err := a.publishNew(context.Background(), "acme", &st)
	require.Error(t, err, "the old store is gone")

	moved, err := a.publishNew(context.Background(), "acme", &st)
	require.NoError(t, err)
	assert.True(t, moved)
	assert.Equal(t, "new-store", st.StoreID)
	assert.Empty(t, st.Token)
	assert.WithinDuration(t, time.Now(), st.Since, time.Minute)

	last := requests[len(requests)-1]
	assert.True(t, strings.HasPrefix(last, "/stores/new-store/changes?"), last)
	assert.NotContains(t, last, "old-token")
}
