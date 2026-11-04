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
	"slices"
	"strings"

	"k8s.io/client-go/rest"
)

// orgsPath is the workspace that holds the Accounts of all orgs.
const orgsPath = "root:orgs"

// errNotFound means kcp does not know the workspace or object. Through the
// virtual workspace an unknown cluster answers 403, not 404.
var errNotFound = errors.New("not found")

// PathResolver finds the kcp workspace path of an account, for example
// root:orgs:acme:team-a. OpenFGA only names the workspace that holds the
// Account object, by cluster id; that workspace's AccountInfo has its path.
// It reads through the virtual workspace of the core.platform-mesh.io export,
// which can get AccountInfo in every account workspace. Not safe for
// concurrent use.
type PathResolver struct {
	// Config reaches the workspace of the APIExportEndpointSlice.
	Config        *rest.Config
	EndpointSlice string

	http      *http.Client
	endpoints []string
	// parents caches the path of a workspace by cluster id. Paths don't change.
	parents map[string]string
}

// Path returns the path of the account in the given cluster, or "" if the
// workspace is gone.
func (r *PathResolver) Path(ctx context.Context, cluster, account string) (string, error) {
	if parent, ok := r.parents[cluster]; ok {
		return parent + ":" + account, nil
	}
	if r.http == nil {
		c, err := rest.HTTPClientFor(r.Config)
		if err != nil {
			return "", err
		}
		r.http, r.parents = c, map[string]string{}
	}
	parent, err := r.parentPath(ctx, cluster, account)
	if errors.Is(err, errNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	r.parents[cluster] = parent
	return parent + ":" + account, nil
}

// parentPath returns the path of the workspace with the given cluster id,
// which holds the account.
func (r *PathResolver) parentPath(ctx context.Context, cluster, account string) (string, error) {
	var info struct {
		Spec struct {
			Account struct {
				Path string `json:"path"`
			} `json:"account"`
		} `json:"spec"`
	}
	err := r.getInCluster(ctx, cluster, "accountinfos/account", &info)
	if err == nil && info.Spec.Account.Path != "" {
		return info.Spec.Account.Path, nil
	}
	if err != nil && !errors.Is(err, errNotFound) {
		return "", err
	}
	// root:orgs has no AccountInfo. It holds the org Accounts and nothing else,
	// so one org Account is enough to know the workspace.
	var acc struct {
		Spec struct {
			Type string `json:"type"`
		} `json:"spec"`
	}
	if err := r.getInCluster(ctx, cluster, "accounts/"+url.PathEscape(account), &acc); err != nil {
		return "", err
	}
	if acc.Spec.Type != "org" {
		return "", errNotFound
	}
	return orgsPath, nil
}

// getInCluster reads a core.platform-mesh.io object in a cluster. With more
// than one shard there is one endpoint per shard; the first that knows the
// cluster answers. If none does, or one fails, the endpoints may have changed
// (a shard was added, an endpoint moved), so they are read again and, if they
// changed, tried once more.
func (r *PathResolver) getInCluster(ctx context.Context, cluster, resource string, out any) error {
	if len(r.endpoints) == 0 {
		if err := r.loadEndpoints(ctx); err != nil {
			return err
		}
	}
	err := r.getFromEndpoints(ctx, cluster, resource, out)
	if err == nil {
		return nil
	}
	known := r.endpoints
	if r.loadEndpoints(ctx) != nil || slices.Equal(known, r.endpoints) {
		return err
	}
	return r.getFromEndpoints(ctx, cluster, resource, out)
}

func (r *PathResolver) getFromEndpoints(ctx context.Context, cluster, resource string, out any) error {
	err := errNotFound
	for _, ep := range r.endpoints {
		err = r.get(ctx, ep+"/clusters/"+url.PathEscape(cluster)+"/apis/core.platform-mesh.io/v1alpha1/"+resource, out)
		if !errors.Is(err, errNotFound) {
			return err
		}
	}
	return err
}

// loadEndpoints reads the endpoints of the APIExportEndpointSlice. On error the
// endpoints known so far stay.
func (r *PathResolver) loadEndpoints(ctx context.Context) error {
	var slice struct {
		Status struct {
			Endpoints []struct {
				URL string `json:"url"`
			} `json:"endpoints"`
		} `json:"status"`
	}
	host := strings.TrimSuffix(r.Config.Host, "/")
	if err := r.get(ctx, host+"/apis/apis.kcp.io/v1alpha1/apiexportendpointslices/"+url.PathEscape(r.EndpointSlice), &slice); err != nil {
		return fmt.Errorf("APIExportEndpointSlice %s: %v", r.EndpointSlice, err) //nolint:errorlint // not found here is an error, not a gone workspace
	}
	endpoints := make([]string, 0, len(slice.Status.Endpoints))
	for _, ep := range slice.Status.Endpoints {
		endpoints = append(endpoints, strings.TrimSuffix(ep.URL, "/"))
	}
	if len(endpoints) == 0 {
		return fmt.Errorf("APIExportEndpointSlice %s has no endpoints yet", r.EndpointSlice)
	}
	r.endpoints = endpoints
	return nil
}

func (r *PathResolver) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	switch resp.StatusCode {
	case http.StatusOK:
		return json.NewDecoder(resp.Body).Decode(out)
	case http.StatusNotFound, http.StatusForbidden:
		return errNotFound
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return fmt.Errorf("GET %s: %s: %s", req.URL.Path, resp.Status, msg)
}
