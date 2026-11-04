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
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"go.platform-mesh.io/golang-commons/logger"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	mcpcache "github.com/kcp-dev/multicluster-provider/pkg/cache"
)

// endpointsReload is how often unknown accounts make us read the
// APIExportEndpointSlice again, in case a shard was added.
const endpointsReload = 30 * time.Second

var accountInfoGVK = schema.GroupVersionKind{Group: "core.platform-mesh.io", Version: "v1alpha1", Kind: "AccountInfo"}

// AccountPaths knows the kcp workspace path of every account, for example
// root:orgs:acme:team-a. Every account workspace has an AccountInfo with the
// path, and the originClusterId and name it gives for the account are our
// account id. AccountPaths watches them in all workspaces through the virtual
// workspace of the core.platform-mesh.io export, one watch per endpoint (shard).
//
// Paths of deleted accounts stay, so the events about their members still
// carry the path. After a restart only the accounts that still exist are known.
type AccountPaths struct {
	// Config reaches the workspace of the APIExportEndpointSlice.
	Config        *rest.Config
	EndpointSlice string
	// Grace is how long the changes of an account that isn't known yet are held
	// back, waiting for its AccountInfo. After that the account counts as gone
	// before we saw it, and its events have no path.
	Grace time.Duration
	Log   *logger.Logger

	// watch starts watching AccountInfos behind an endpoint and reports whether
	// the watch has seen all of them. Tests replace it.
	watch func(ctx context.Context, endpoint string) (synced func() bool, err error)
	now   func() time.Time

	ctx      context.Context
	http     *http.Client
	synced   map[string]func() bool // by endpoint
	loadedAt time.Time

	mu      sync.Mutex
	paths   map[string]string    // by account id
	unknown map[string]time.Time // by account id: when it was first asked for
}

// Start prepares the watches; they run until ctx is done. The endpoints are
// read on the first Path.
func (p *AccountPaths) Start(ctx context.Context) error {
	c, err := rest.HTTPClientFor(p.Config)
	if err != nil {
		return err
	}
	p.ctx, p.http = ctx, c
	p.synced = map[string]func() bool{}
	p.paths, p.unknown = map[string]string{}, map[string]time.Time{}
	if p.watch == nil {
		p.watch = p.watchEndpoint
	}
	if p.now == nil {
		p.now = time.Now
	}
	return nil
}

// Path returns the path of the account in the given cluster, the cluster that
// holds the Account object. It returns an error while the account isn't known
// yet but may still show up; the caller tries again later. Not safe for
// concurrent use.
func (p *AccountPaths) Path(ctx context.Context, cluster, account string) (string, error) {
	id := contract.AccountID(cluster, account)
	if path, ok := p.lookup(id); ok {
		return path, nil
	}
	// A new account whose AccountInfo hasn't arrived yet, an account on a shard
	// we don't watch yet, or one deleted before we saw it.
	if err := p.watchEndpoints(ctx); err != nil {
		return "", err
	}
	for _, synced := range p.synced {
		if !synced() {
			return "", errors.New("account paths are not loaded yet")
		}
	}
	if path, ok := p.lookup(id); ok {
		return path, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	since, ok := p.unknown[id]
	if !ok {
		since = p.now()
		p.unknown[id] = since
	}
	if p.now().Sub(since) < p.Grace {
		return "", fmt.Errorf("no AccountInfo for account %s yet", id)
	}
	return "", nil
}

func (p *AccountPaths) lookup(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	path, ok := p.paths[id]
	return path, ok
}

// add records the path from an AccountInfo.
func (p *AccountPaths) add(obj any) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	name, _, _ := unstructured.NestedString(u.Object, "spec", "account", "name")
	cluster, _, _ := unstructured.NestedString(u.Object, "spec", "account", "originClusterId")
	path, _, _ := unstructured.NestedString(u.Object, "spec", "account", "path")
	if name == "" || cluster == "" || path == "" {
		return
	}
	id := contract.AccountID(cluster, name)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths[id] = path
	delete(p.unknown, id)
}

// watchEndpoints reads the APIExportEndpointSlice and watches endpoints that
// are new. It reads the slice at most every endpointsReload; if that fails,
// the endpoints watched so far stay.
func (p *AccountPaths) watchEndpoints(ctx context.Context) error {
	if len(p.synced) > 0 && p.now().Sub(p.loadedAt) < endpointsReload {
		return nil
	}
	endpoints, err := p.loadEndpoints(ctx)
	if err != nil {
		if len(p.synced) > 0 {
			p.Log.Warn().Err(err).Msg("cannot read the core endpoints again")
			return nil
		}
		return err
	}
	p.loadedAt = p.now()
	for _, ep := range endpoints {
		if _, ok := p.synced[ep]; ok {
			continue
		}
		synced, err := p.watch(p.ctx, ep)
		if err != nil {
			return fmt.Errorf("watching AccountInfos at %s: %w", ep, err)
		}
		p.synced[ep] = synced
	}
	return nil
}

// watchEndpoint watches the AccountInfos of all workspaces behind one endpoint.
func (p *AccountPaths) watchEndpoint(ctx context.Context, endpoint string) (func() bool, error) {
	cfg := rest.CopyConfig(p.Config)
	cfg.Host = endpoint
	// A fixed mapping, so the cache doesn't need discovery.
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.Add(accountInfoGVK, meta.RESTScopeRoot)
	c, err := mcpcache.NewWildcardCache(cfg, cache.Options{Mapper: mapper})
	if err != nil {
		return nil, err
	}
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(accountInfoGVK)
	inf, _, _, err := c.GetSharedInformer(obj)
	if err != nil {
		return nil, err
	}
	// Deletions are left out on purpose: the path of a deleted account stays.
	reg, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    p.add,
		UpdateFunc: func(_, obj any) { p.add(obj) },
	})
	if err != nil {
		return nil, err
	}
	go func() {
		if err := c.Start(ctx); err != nil {
			p.Log.Error().Err(err).Str("endpoint", endpoint).Msg("AccountInfo watch stopped")
		}
	}()
	return reg.HasSynced, nil
}

func (p *AccountPaths) loadEndpoints(ctx context.Context) ([]string, error) {
	var slice struct {
		Status struct {
			Endpoints []struct {
				URL string `json:"url"`
			} `json:"endpoints"`
		} `json:"status"`
	}
	host := strings.TrimSuffix(p.Config.Host, "/")
	if err := p.get(ctx, host+"/apis/apis.kcp.io/v1alpha1/apiexportendpointslices/"+url.PathEscape(p.EndpointSlice), &slice); err != nil {
		return nil, fmt.Errorf("APIExportEndpointSlice %s: %w", p.EndpointSlice, err)
	}
	endpoints := make([]string, 0, len(slice.Status.Endpoints))
	for _, ep := range slice.Status.Endpoints {
		endpoints = append(endpoints, strings.TrimSuffix(ep.URL, "/"))
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("APIExportEndpointSlice %s has no endpoints yet", p.EndpointSlice)
	}
	return endpoints, nil
}

func (p *AccountPaths) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("GET %s: %s: %s", req.URL.Path, resp.Status, msg)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
