/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package openfga

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.platform-mesh.io/golang-commons/logger/testlogger"

	"k8s.io/client-go/rest"
)

// fakeKCP serves the APIExportEndpointSlice and, behind its virtual
// workspace, AccountInfos and Accounts by cluster id. Unknown clusters get 403,
// like through the real virtual workspace.
type fakeKCP struct {
	srv *httptest.Server
	// infos is the AccountInfo path per cluster, accounts the Account types.
	infos    map[string]string
	accounts map[string]string // cluster/name -> type
	broken   bool
	requests int
}

const vwPrefix = "/services/apiexport/c-core/core.platform-mesh.io/clusters/"

func (f *fakeKCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/clusters/root:platform-mesh-system/apis/apis.kcp.io/v1alpha1/apiexportendpointslices/core.platform-mesh.io" {
		_, _ = fmt.Fprintf(w, `{"status": {"endpoints": [{"url": "%s/services/apiexport/c-core/core.platform-mesh.io"}]}}`, f.srv.URL)
		return
	}
	f.requests++
	if f.broken {
		http.Error(w, "etcd down", http.StatusInternalServerError)
		return
	}
	cluster, resource, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, vwPrefix), "/apis/core.platform-mesh.io/v1alpha1/")
	if !strings.HasPrefix(r.URL.Path, vwPrefix) || !ok {
		http.NotFound(w, r)
		return
	}
	if _, known := f.infos[cluster]; !known && cluster != "c-orgs" {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}
	switch {
	case resource == "accountinfos/account" && f.infos[cluster] != "":
		_, _ = fmt.Fprintf(w, `{"spec": {"account": {"path": %q}}}`, f.infos[cluster])
	case strings.HasPrefix(resource, "accounts/") && f.accounts[cluster+"/"+strings.TrimPrefix(resource, "accounts/")] != "":
		_, _ = fmt.Fprintf(w, `{"spec": {"type": %q}}`, f.accounts[cluster+"/"+strings.TrimPrefix(resource, "accounts/")])
	default:
		http.NotFound(w, r)
	}
}

func newFakeKCP(t *testing.T) (*fakeKCP, *PathResolver) {
	f := &fakeKCP{
		infos:    map[string]string{"c-acme": "root:orgs:acme", "c-team-a": "root:orgs:acme:team-a"},
		accounts: map[string]string{"c-orgs/acme": "org", "c-acme/team-a": "account"},
	}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	return f, &PathResolver{
		Config:        &rest.Config{Host: f.srv.URL + "/clusters/root:platform-mesh-system/"},
		EndpointSlice: "core.platform-mesh.io",
	}
}

func TestPathResolver(t *testing.T) {
	f, r := newFakeKCP(t)
	ctx := context.Background()

	path, err := r.Path(ctx, "c-orgs", "acme")
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme", path, "org accounts live in root:orgs")

	path, err = r.Path(ctx, "c-acme", "team-a")
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme:team-a", path)

	path, err = r.Path(ctx, "c-team-a", "team-a")
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme:team-a:team-a", path, "nested accounts can share a name")

	// Paths are cached by cluster: another account in a known workspace needs no request.
	before := f.requests
	path, err = r.Path(ctx, "c-acme", "team-b")
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme:team-b", path)
	assert.Equal(t, before, f.requests)

	// A workspace that is gone has no path.
	path, err = r.Path(ctx, "c-gone", "team-x")
	require.NoError(t, err)
	assert.Empty(t, path)

	// A workspace without AccountInfo is root:orgs only if it holds an org Account.
	_, fresh := newFakeKCP(t)
	path, err = fresh.Path(ctx, "c-orgs", "not-an-org")
	require.NoError(t, err)
	assert.Empty(t, path)

	// Other errors are returned, so the page is read again later.
	f.broken = true
	_, err = r.Path(ctx, "c-other", "team-a")
	require.Error(t, err)
}

func TestPublishWithPaths(t *testing.T) {
	_, paths := newFakeKCP(t)
	fga := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/read") {
			_, _ = w.Write([]byte(`{"tuples": []}`))
			return
		}
		_, _ = w.Write([]byte(`{"changes": [
		  {"operation": "TUPLE_OPERATION_WRITE", "timestamp": "2026-10-23T13:43:24Z",
		   "tuple_key": {"user": "user:alice@example.com", "relation": "assignee", "object": "role:core_platform-mesh_io_account/c-acme/team-a/member"}}
		]}`))
	}))
	defer fga.Close()

	var data []string
	a := &Adapter{
		FGA:   &Client{BaseURL: fga.URL, HTTP: fga.Client()},
		Paths: paths,
		Log:   testlogger.New().HideLogOutput().Logger,
		Publish: func(_ context.Context, e cloudevents.Event) error {
			data = append(data, string(e.Data()))
			return nil
		},
	}
	_, err := a.publishNew(context.Background(), "acme", &orgState{StoreID: "s"})
	require.NoError(t, err)
	require.Len(t, data, 2)
	for _, d := range data {
		assert.Contains(t, d, `"accountPath":"root:orgs:acme:team-a"`)
	}
}
