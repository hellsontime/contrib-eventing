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
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.platform-mesh.io/golang-commons/logger/testlogger"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

// fakeKCP serves the APIExportEndpointSlice. The watches behind its endpoints
// are stubbed: tests hand AccountInfos to the paths directly, the way the
// watch's event handler does.
type fakeKCP struct {
	srv *httptest.Server
	// exports are the ids in the virtual workspace URLs, one per shard.
	exports []string
	broken  bool
	reads   int
	// watched are the endpoints a watch was started for, synced whether they
	// have seen all AccountInfos.
	watched []string
	synced  bool
}

func (f *fakeKCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/clusters/root:platform-mesh-system/apis/apis.kcp.io/v1alpha1/apiexportendpointslices/core.platform-mesh.io" || f.broken {
		http.Error(w, "nope", http.StatusInternalServerError)
		return
	}
	f.reads++
	urls := make([]string, 0, len(f.exports))
	for _, e := range f.exports {
		urls = append(urls, fmt.Sprintf(`{"url": "%s/services/apiexport/%s/core.platform-mesh.io"}`, f.srv.URL, e))
	}
	_, _ = fmt.Fprintf(w, `{"status": {"endpoints": [%s]}}`, strings.Join(urls, ","))
}

// newFakeKCP returns paths that know nothing yet, at a fixed time.
func newFakeKCP(t *testing.T) (*fakeKCP, *AccountPaths, *time.Time) {
	f := &fakeKCP{exports: []string{"c-core"}, synced: true}
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	now := time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)
	p := &AccountPaths{
		Config:        &rest.Config{Host: f.srv.URL + "/clusters/root:platform-mesh-system/"},
		EndpointSlice: "core.platform-mesh.io",
		Grace:         time.Minute,
		Log:           testlogger.New().HideLogOutput().Logger,
		watch: func(_ context.Context, endpoint string) (func() bool, error) {
			f.watched = append(f.watched, endpoint)
			return func() bool { return f.synced }, nil
		},
		now: func() time.Time { return now },
	}
	require.NoError(t, p.Start(context.Background()))
	return f, p, &now
}

// accountInfo is an AccountInfo as the watch hands it over.
func accountInfo(cluster, name, path string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "core.platform-mesh.io/v1alpha1",
		"kind":       "AccountInfo",
		"metadata":   map[string]any{"name": "account"},
		"spec": map[string]any{"account": map[string]any{
			"name": name, "originClusterId": cluster, "path": path,
		}},
	}}
}

func TestAccountPaths(t *testing.T) {
	f, p, _ := newFakeKCP(t)
	ctx := context.Background()

	// Orgs and nested accounts alike: each has its own AccountInfo.
	p.add(accountInfo("c-orgs", "acme", "root:orgs:acme"))
	p.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
	p.add(accountInfo("c-team-a", "team-a", "root:orgs:acme:team-a:team-a"))
	for id, want := range map[[2]string]string{
		{"c-orgs", "acme"}:     "root:orgs:acme",
		{"c-acme", "team-a"}:   "root:orgs:acme:team-a",
		{"c-team-a", "team-a"}: "root:orgs:acme:team-a:team-a",
	} {
		path, err := p.Path(ctx, id[0], id[1])
		require.NoError(t, err)
		assert.Equal(t, want, path, id)
	}
	assert.Empty(t, f.watched, "known accounts need no endpoints")

	// An AccountInfo without the fields we need is ignored.
	p.add(accountInfo("c-acme", "", "root:orgs:acme:nameless"))
	assert.Len(t, p.paths, 3)
}

func TestAccountPathsUnknown(t *testing.T) {
	f, p, now := newFakeKCP(t)
	ctx := context.Background()

	// Until every watch has seen all AccountInfos, nothing is decided.
	f.synced = false
	_, err := p.Path(ctx, "c-acme", "team-a")
	require.ErrorContains(t, err, "not loaded yet")
	assert.Equal(t, []string{f.srv.URL + "/services/apiexport/c-core/core.platform-mesh.io"}, f.watched)

	// A new account whose AccountInfo hasn't arrived yet: try again later.
	f.synced = true
	_, err = p.Path(ctx, "c-acme", "team-a")
	require.ErrorContains(t, err, "no AccountInfo for account c-acme/team-a yet")
	assert.Equal(t, 1, f.reads, "the slice is read again only after a while")
	*now = now.Add(30 * time.Second)
	p.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
	path, err := p.Path(ctx, "c-acme", "team-a")
	require.NoError(t, err)
	assert.Equal(t, "root:orgs:acme:team-a", path)

	// An account that never shows up was deleted before we saw it: no path
	// once the grace period is over.
	_, err = p.Path(ctx, "c-acme", "gone")
	require.Error(t, err)
	*now = now.Add(time.Minute)
	path, err = p.Path(ctx, "c-acme", "gone")
	require.NoError(t, err)
	assert.Empty(t, path)
}

func TestAccountPathsEndpointsChange(t *testing.T) {
	f, p, now := newFakeKCP(t)
	ctx := context.Background()

	_, err := p.Path(ctx, "c-acme", "team-a")
	require.Error(t, err)
	require.Len(t, f.watched, 1)

	// A shard was added. The next unknown account after a while reads the
	// slice again, and the new endpoint is watched as well.
	f.exports = append(f.exports, "c-shard-2")
	*now = now.Add(endpointsReload)
	_, err = p.Path(ctx, "c-shard-2-ws", "team-b")
	require.Error(t, err)
	assert.Equal(t, 2, f.reads)
	assert.Equal(t, []string{
		f.srv.URL + "/services/apiexport/c-core/core.platform-mesh.io",
		f.srv.URL + "/services/apiexport/c-shard-2/core.platform-mesh.io",
	}, f.watched)

	// If the slice can't be read again, the watches so far go on.
	f.broken = true
	*now = now.Add(endpointsReload)
	_, err = p.Path(ctx, "c-x", "team-x")
	require.ErrorContains(t, err, "no AccountInfo")
}

func TestAccountPathsNoSlice(t *testing.T) {
	f, p, _ := newFakeKCP(t)
	f.broken = true
	_, err := p.Path(context.Background(), "c-acme", "team-a")
	require.ErrorContains(t, err, "APIExportEndpointSlice core.platform-mesh.io")
	assert.Empty(t, f.watched)
}

func TestPublishWithPaths(t *testing.T) {
	_, paths, _ := newFakeKCP(t)
	paths.add(accountInfo("c-acme", "team-a", "root:orgs:acme:team-a"))
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
