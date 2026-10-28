/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	corev1 "k8s.io/api/core/v1"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const twoChanges = `{"changes": [
  {"operation": "TUPLE_OPERATION_WRITE", "timestamp": "2026-10-23T13:43:24Z",
   "tuple_key": {"user": "user:alice@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c1/team-a/member"}},
  {"operation": "TUPLE_OPERATION_DELETE", "timestamp": "2026-10-23T13:43:25Z",
   "tuple_key": {"user": "user:new@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c2/team-a/owner"}}
], "continuation_token": "after-page"}`

func TestPublishNew(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("continuation_token") == "after-page" {
			_, _ = w.Write([]byte(`{"changes": [], "continuation_token": "after-page"}`))
			return
		}
		_, _ = w.Write([]byte(twoChanges))
	}))
	defer srv.Close()

	var published []cloudevents.Event
	fail := true
	a := &Adapter{
		FGA: &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Log: testlogger.New().HideLogOutput().Logger,
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
	assert.Equal(t, contract.User{ID: "alice@example.com"}, granted.User)
	assert.Equal(t, "c1/team-a", granted.AccountID)
	assert.Equal(t, "accounts/c1/team-a", published[0].Subject())
	// Same name, other parent workspace: another account.
	assert.Equal(t, contract.User{ID: "new@example.com"}, revoked.User)
	assert.Equal(t, "accounts/c2/team-a", published[1].Subject())
	assert.Equal(t, contract.TypeMembershipRevoked, published[1].Type())
	assert.Equal(t, "owner", revoked.Role)
}

// fakeFGA serves a list of stores and answers every changelog request with no
// changes, recording what was asked for.
type fakeFGA struct {
	stores   map[string]string // name -> id
	requests []string
}

func (f *fakeFGA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/stores" {
		list := make([]string, 0, len(f.stores))
		for name, id := range f.stores {
			list = append(list, fmt.Sprintf(`{"id": %q, "name": %q}`, id, name))
		}
		_, _ = fmt.Fprintf(w, `{"stores": [%s]}`, strings.Join(list, ","))
		return
	}
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
	_, _ = w.Write([]byte(`{"changes": []}`))
}

func (f *fakeFGA) asked(storeID string) []string {
	var out []string
	for _, r := range f.requests {
		if strings.HasPrefix(r, "/stores/"+storeID+"/") {
			out = append(out, r)
		}
	}
	return out
}

func loadedState(t *testing.T, a *Adapter) map[string]orgState {
	_, state, err := a.loadState(context.Background())
	require.NoError(t, err)
	return state
}

func TestPollOrgs(t *testing.T) {
	fga := &fakeFGA{stores: map[string]string{"orgs": "s-root", "acme": "s-acme"}}
	srv := httptest.NewServer(fga)
	defer srv.Close()
	a := &Adapter{
		FGA:        &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		SkipStores: []string{"orgs"},
		Kube:       fake.NewClientBuilder().Build(),
		Namespace:  "contrib-eventing",
		Log:        testlogger.New().HideLogOutput().Logger,
	}
	ctx := context.Background()

	// First run: existing orgs start now, the store of root:orgs is skipped.
	require.NoError(t, a.poll(ctx))
	assert.Empty(t, fga.asked("s-root"))
	require.Len(t, fga.asked("s-acme"), 1)
	assert.Contains(t, fga.asked("s-acme")[0], "start_time=")
	assert.WithinDuration(t, time.Now(), loadedState(t, a)["acme"].Since, time.Minute)

	// An org created later starts at the beginning of its store.
	fga.stores["beta"] = "s-beta"
	require.NoError(t, a.poll(ctx))
	require.Len(t, fga.asked("s-beta"), 1)
	assert.NotContains(t, fga.asked("s-beta")[0], "start_time=")

	// A recreated org gets a new store and starts fresh; a deleted one is forgotten.
	fga.stores["acme"] = "s-acme-2"
	delete(fga.stores, "beta")
	require.NoError(t, a.poll(ctx))
	state := loadedState(t, a)
	assert.Equal(t, "s-acme-2", state["acme"].StoreID)
	assert.NotContains(t, state, "beta")
	assert.Len(t, fga.asked("s-acme-2"), 1)

	cm := &corev1.ConfigMap{}
	require.NoError(t, a.Kube.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: "contrib-eventing", Name: StateConfigMap}, cm))
	assert.Len(t, cm.Data, 1)
}

// When the first run finds no org at all, the first org created afterwards
// must still start at the beginning of its store.
func TestPollFirstOrgAfterEmptyStart(t *testing.T) {
	fga := &fakeFGA{stores: map[string]string{"orgs": "s-root"}}
	srv := httptest.NewServer(fga)
	defer srv.Close()
	a := &Adapter{
		FGA:        &Client{BaseURL: srv.URL, HTTP: srv.Client()},
		SkipStores: []string{"orgs"},
		Kube:       fake.NewClientBuilder().Build(),
		Namespace:  "contrib-eventing",
		Log:        testlogger.New().HideLogOutput().Logger,
	}
	require.NoError(t, a.poll(context.Background()))

	fga.stores["acme"] = "s-acme"
	require.NoError(t, a.poll(context.Background()))
	require.Len(t, fga.asked("s-acme"), 1)
	assert.NotContains(t, fga.asked("s-acme")[0], "start_time=")
}
