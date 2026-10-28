/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package ingress

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"

	"go.platform-mesh.io/golang-commons/logger/testlogger"
)

type fakeAuth map[string]string

func (f fakeAuth) Authenticate(_ context.Context, token string) (string, error) {
	if user, ok := f[token]; ok {
		return user, nil
	}
	return "", errors.New("unknown token")
}

func TestHandler(t *testing.T) {
	var forwarded []string
	h := &Handler{
		Auth:       fakeAuth{"adapter": "system:serviceaccount:contrib-eventing:openfga-adapter", "other": "system:serviceaccount:default:x"},
		Publishers: []string{"system:serviceaccount:contrib-eventing:openfga-adapter"},
		Log:        testlogger.New().HideLogOutput().Logger,
		Forward: func(_ context.Context, e cloudevents.Event) error {
			forwarded = append(forwarded, e.Extensions()["pmorg"].(string))
			return nil
		},
	}

	for _, tc := range []struct {
		name, token, org string
		want             int
	}{
		{"no token", "", "acme", http.StatusUnauthorized},
		{"unknown token", "forged", "acme", http.StatusUnauthorized},
		{"not a publisher", "other", "acme", http.StatusForbidden},
		{"no org", "adapter", "", http.StatusBadRequest},
		{"accepted", "adapter", "acme", http.StatusAccepted},
	} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
		req.Header.Set("Ce-Specversion", "1.0")
		req.Header.Set("Ce-Id", "1")
		req.Header.Set("Ce-Type", "t")
		req.Header.Set("Ce-Source", "s")
		req.Header.Set("Content-Type", "application/json")
		if tc.org != "" {
			req.Header.Set("Ce-Pmorg", tc.org)
		}
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, tc.want, rec.Code, "%s: %s", tc.name, rec.Body)
	}
	assert.Equal(t, []string{"acme"}, forwarded)
}
