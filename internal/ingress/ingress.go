/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package ingress is the only way into the brokers. It checks who sends an
// event before handing it to the broker of the event's org; the brokers
// themselves accept anything that reaches them, so a NetworkPolicy keeps
// everyone else out.
package ingress

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"
	"go.platform-mesh.io/golang-commons/logger"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Authenticator returns the user a bearer token belongs to.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (string, error)
}

type Handler struct {
	Auth Authenticator
	// Publishers may send events for any org. For now these are the platform's
	// own adapters; providers publishing for their orgs come later.
	Publishers []string
	Kube       ctrlruntimeclient.Client
	// Forward hands an accepted event to the org's broker.
	Forward func(ctx context.Context, org string, e cloudevents.Event) error
	Log     *logger.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "only POST", http.StatusMethodNotAllowed)
		return
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	sender, err := h.Auth.Authenticate(r.Context(), token)
	if err != nil {
		h.Log.Info().Err(err).Msg("rejected unauthenticated request")
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if !slices.Contains(h.Publishers, sender) {
		h.Log.Info().Str("sender", sender).Msg("rejected sender")
		http.Error(w, sender+" may not publish", http.StatusForbidden)
		return
	}

	e, err := cehttp.NewEventFromHTTPRequest(r)
	if err != nil {
		http.Error(w, "not a CloudEvent: "+err.Error(), http.StatusBadRequest)
		return
	}
	org, _ := e.Extensions()[contract.OrgExtension].(string)
	if org == "" {
		http.Error(w, "missing extension "+contract.OrgExtension, http.StatusBadRequest)
		return
	}

	// An org without a broker has no Subscriptions. The event is accepted
	// and dropped, so publishers don't retry it.
	if err := h.Kube.Get(r.Context(), ctrlruntimeclient.ObjectKey{Name: orgs.Namespace(org)}, &corev1.Namespace{}); err != nil {
		if apierrors.IsNotFound(err) {
			h.Log.Info().Str("org", org).Str("id", e.ID()).Msg("dropped event, org has no subscriptions")
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Forward(r.Context(), org, *e); err != nil {
		h.Log.Error().Err(err).Str("org", org).Str("id", e.ID()).Msg("forwarding failed")
		http.Error(w, "broker did not accept the event", http.StatusBadGateway)
		return
	}
	h.Log.Info().Str("sender", sender).Str("org", org).Str("type", e.Type()).Str("id", e.ID()).Msg("forwarded")
	w.WriteHeader(http.StatusAccepted)
}

// TokenReviewer authenticates Kubernetes ServiceAccount tokens that were
// issued for Audience.
type TokenReviewer struct {
	Kube     ctrlruntimeclient.Client
	Audience string
}

func (t *TokenReviewer) Authenticate(ctx context.Context, token string) (string, error) {
	review := &authenticationv1.TokenReview{Spec: authenticationv1.TokenReviewSpec{Token: token, Audiences: []string{t.Audience}}}
	if err := t.Kube.Create(ctx, review); err != nil {
		return "", err
	}
	if !review.Status.Authenticated {
		return "", errors.New(review.Status.Error)
	}
	if !slices.Contains(review.Status.Audiences, t.Audience) {
		return "", errors.New("token is not meant for " + t.Audience)
	}
	return review.Status.User.Username, nil
}
