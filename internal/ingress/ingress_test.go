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

	"go.platform-mesh.io/golang-commons/logger/testlogger"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeAuth map[string]string

func (f fakeAuth) Authenticate(_ context.Context, token string) (string, error) {
	if user, ok := f[token]; ok {
		return user, nil
	}
	return "", errors.New("unknown token")
}

func TestHandler(t *testing.T) {
	broker := &corev1.Namespace{}
	broker.Name = "pm-events-acme"
	var forwarded []string
	h := &Handler{
		Auth:       fakeAuth{"adapter": "system:serviceaccount:contrib-eventing:openfga-adapter", "other": "system:serviceaccount:default:x"},
		Publishers: []string{"system:serviceaccount:contrib-eventing:openfga-adapter"},
		Kube:       fake.NewClientBuilder().WithObjects(broker).Build(),
		Log:        testlogger.New().HideLogOutput().Logger,
		Forward: func(_ context.Context, org string, _ cloudevents.Event) error {
			forwarded = append(forwarded, org)
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
		{"org without subscriptions", "adapter", "nobody", http.StatusAccepted},
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
		if rec.Code != tc.want {
			t.Errorf("%s: got %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
	if len(forwarded) != 1 || forwarded[0] != "acme" {
		t.Errorf("forwarded = %v, want only acme", forwarded)
	}
}
