/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package keycloak looks up users in the Keycloak admin API.
package keycloak

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/oauth2/clientcredentials"
)

// Users finds users with a service account client of the master realm.
type Users struct {
	baseURL string
	http    *http.Client
}

func NewUsers(ctx context.Context, baseURL, clientID, clientSecret string) *Users {
	cc := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     baseURL + "/realms/master/protocol/openid-connect/token",
	}
	return &Users{baseURL: baseURL, http: cc.Client(ctx)}
}

// IDByEmail returns the id of the user with this email in the realm, or "" if
// there is no such user. The id is the subject of the user's tokens.
func (u *Users) IDByEmail(ctx context.Context, realm, email string) (string, error) {
	q := url.Values{"email": {email}, "exact": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		u.baseURL+"/admin/realms/"+url.PathEscape(realm)+"/users?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := u.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("looking up user in realm %s: %s: %s", realm, resp.Status, msg)
	}
	var users []struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return "", err
	}
	if len(users) == 0 {
		return "", nil
	}
	return users[0].ID, nil
}
