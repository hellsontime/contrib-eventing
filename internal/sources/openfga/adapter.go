/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package openfga publishes membership events read from the OpenFGA changelog.
// OpenFGA cannot send events itself, but its changelog contains every tuple
// write, no matter which component made it.
package openfga

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/go-logr/logr"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// StateConfigMap holds, per org, how far the adapter got in the changelog.
	StateConfigMap = "openfga-adapter-state"
	pageSize       = 50
)

// UserIDs resolves an email to the user's id in the org's realm, or "" if the
// user does not exist there yet.
type UserIDs interface {
	IDByEmail(ctx context.Context, realm, email string) (string, error)
}

type Adapter struct {
	FGA       *Client
	Users     UserIDs
	Kube      ctrlruntimeclient.Client
	Namespace string
	Publish   func(ctx context.Context, e cloudevents.Event) error
	Interval  time.Duration
	Log       logr.Logger

	storeIDs map[string]string
}

// orgState is stored as JSON in the state ConfigMap, one key per org.
type orgState struct {
	// StoreID is the org's OpenFGA store. A recreated org gets a new store, and
	// tokens of the old one don't work there.
	StoreID string `json:"storeID,omitempty"`
	// Token is the OpenFGA continuation token after the last published change.
	Token string `json:"token,omitempty"`
	// Since is when the org subscribed. Changes before it are not published.
	Since time.Time `json:"since"`
}

func (a *Adapter) Run(ctx context.Context) error {
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		if err := a.poll(ctx); err != nil {
			a.Log.Error(err, "poll failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (a *Adapter) poll(ctx context.Context) error {
	// Orgs with a Subscription have a broker namespace.
	var nss corev1.NamespaceList
	if err := a.Kube.List(ctx, &nss, ctrlruntimeclient.HasLabels{orgs.Label}); err != nil {
		return err
	}
	cm, state, err := a.loadState(ctx)
	if err != nil {
		return err
	}
	changed := false
	for _, ns := range nss.Items {
		org := ns.Labels[orgs.Label]
		st, known := state[org]
		if !known {
			st = orgState{Since: time.Now()}
			changed = true
		}
		moved, err := a.publishNew(ctx, org, &st)
		if err != nil {
			a.Log.Error(err, "cannot publish membership changes", "org", org)
		}
		if moved || !known {
			state[org] = st
			changed = true
		}
	}
	if changed {
		return a.saveState(ctx, cm, state)
	}
	return nil
}

// publishNew publishes the org's new membership changes page by page. The
// token only moves after every event of a page was accepted; if one fails, the
// whole page is read again next time. Consumers drop the repeats by event id.
func (a *Adapter) publishNew(ctx context.Context, org string, st *orgState) (bool, error) {
	storeID, err := a.storeID(ctx, org)
	if err != nil {
		return false, err
	}
	moved := false
	if st.StoreID != storeID {
		// A new store, for example because the org was deleted and created
		// again. Start at the current time, like for a newly subscribed org.
		*st = orgState{StoreID: storeID, Since: time.Now()}
		moved = true
	}
	for {
		changes, next, err := a.FGA.Changes(ctx, storeID, "role", st.Token, st.Since, pageSize)
		if err != nil {
			// The store may be gone; look it up again next time.
			delete(a.storeIDs, org)
			return moved, err
		}
		for _, c := range changes {
			if err := a.publishChange(ctx, org, c); err != nil {
				return moved, err
			}
		}
		if next != "" && next != st.Token {
			st.Token = next
			moved = true
		}
		if len(changes) < pageSize {
			return moved, nil
		}
	}
}

func (a *Adapter) publishChange(ctx context.Context, org string, c Change) error {
	m, ok := parseMembership(c)
	if !ok {
		return nil
	}
	// Events identify users by id. Only users without a Keycloak account
	// (invited, never logged in) are identified by email.
	user := contract.User{}
	id, err := a.Users.IDByEmail(ctx, org, m.email)
	if err != nil {
		return err
	}
	if id != "" {
		user.ID = id
	} else {
		user.Email = m.email
	}
	granted := c.Operation == opWrite
	key := fmt.Sprintf("openfga/%s/%s/%s/%s/%s", c.Operation, c.TupleKey.User, c.TupleKey.Relation, c.TupleKey.Object, c.Timestamp.Format(time.RFC3339Nano))
	e, err := contract.MembershipChanged(granted, contract.Membership{Org: org, Account: m.account, Role: m.role, User: user}, key, c.Timestamp)
	if err != nil {
		return err
	}
	if err := a.Publish(ctx, e); err != nil {
		return err
	}
	a.Log.Info("published", "org", org, "type", e.Type(), "id", e.ID())
	return nil
}

func (a *Adapter) storeID(ctx context.Context, org string) (string, error) {
	if id, ok := a.storeIDs[org]; ok {
		return id, nil
	}
	ids, err := a.FGA.StoreIDs(ctx)
	if err != nil {
		return "", err
	}
	a.storeIDs = ids
	if id, ok := ids[org]; ok {
		return id, nil
	}
	return "", fmt.Errorf("no OpenFGA store for org %s", org)
}

func (a *Adapter) loadState(ctx context.Context) (*corev1.ConfigMap, map[string]orgState, error) {
	cm := &corev1.ConfigMap{}
	state := map[string]orgState{}
	err := a.Kube.Get(ctx, ctrlruntimeclient.ObjectKey{Namespace: a.Namespace, Name: StateConfigMap}, cm)
	if apierrors.IsNotFound(err) {
		cm.Namespace, cm.Name = a.Namespace, StateConfigMap
		return cm, state, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for org, raw := range cm.Data {
		var st orgState
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			a.Log.Error(err, "ignoring broken state", "org", org)
			continue
		}
		state[org] = st
	}
	return cm, state, nil
}

func (a *Adapter) saveState(ctx context.Context, cm *corev1.ConfigMap, state map[string]orgState) error {
	cm.Data = map[string]string{}
	for org, st := range state {
		b, err := json.Marshal(st)
		if err != nil {
			return err
		}
		cm.Data[org] = string(b)
	}
	if cm.ResourceVersion == "" {
		return a.Kube.Create(ctx, cm)
	}
	return a.Kube.Update(ctx, cm)
}
