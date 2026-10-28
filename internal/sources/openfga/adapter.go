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
	"slices"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/platform-mesh/contrib-eventing/internal/contract"
	"go.platform-mesh.io/golang-commons/logger"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// StateConfigMap holds, per org, how far the adapter got in the changelog.
	StateConfigMap = "openfga-adapter-state"
	pageSize       = 50
)

// Adapter reads the changelog of every org's OpenFGA store. Platform Mesh
// gives each org a store named like the org.
type Adapter struct {
	FGA *Client
	// SkipStores are stores that do not belong to an org.
	SkipStores []string
	Kube       ctrlruntimeclient.Client
	Namespace  string
	Publish    func(ctx context.Context, e cloudevents.Event) error
	Interval   time.Duration
	Log        *logger.Logger
}

// orgState is stored as JSON in the state ConfigMap, one key per org.
type orgState struct {
	// StoreID is the org's OpenFGA store. A recreated org gets a new store, and
	// tokens of the old one don't work there.
	StoreID string `json:"storeID,omitempty"`
	// Token is the OpenFGA continuation token after the last published change.
	Token string `json:"token,omitempty"`
	// Since is where reading starts while there is no token yet. Zero means the
	// beginning of the store.
	Since time.Time `json:"since,omitzero"`
}

func (a *Adapter) Run(ctx context.Context) error {
	t := time.NewTicker(a.Interval)
	defer t.Stop()
	for {
		if err := a.poll(ctx); err != nil {
			a.Log.Error().Err(err).Msg("poll failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (a *Adapter) poll(ctx context.Context) error {
	stores, err := a.FGA.StoreIDs(ctx)
	if err != nil {
		return err
	}
	cm, state, err := a.loadState(ctx)
	if err != nil {
		return err
	}
	// On the very first run there is no state yet: orgs that exist already start
	// now. Orgs created later start at the beginning of their store, so their
	// first changes, like the owner of a new org, are not missed.
	firstRun := cm.ResourceVersion == ""

	// The first run always saves, even without orgs, so the next run knows it
	// is not the first.
	changed := firstRun
	for org, storeID := range stores {
		if slices.Contains(a.SkipStores, org) {
			continue
		}
		st, known := state[org]
		fresh := !known || st.StoreID != storeID
		if fresh {
			st = orgState{StoreID: storeID}
			if firstRun {
				st.Since = time.Now()
			}
		}
		moved, err := a.publishNew(ctx, org, &st)
		if err != nil {
			a.Log.Error().Err(err).Str("org", org).Msg("cannot publish membership changes")
		}
		if moved || fresh {
			state[org] = st
			changed = true
		}
	}
	// Forget orgs whose store is gone.
	for org := range state {
		if _, ok := stores[org]; !ok {
			delete(state, org)
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
	moved := false
	for {
		changes, next, err := a.FGA.Changes(ctx, st.StoreID, "role", st.Token, st.Since, pageSize)
		if err != nil {
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
	granted := c.Operation == opWrite
	key := fmt.Sprintf("openfga/%s/%s/%s/%s/%s", c.Operation, c.TupleKey.User, c.TupleKey.Relation, c.TupleKey.Object, c.Timestamp.Format(time.RFC3339Nano))
	e, err := contract.MembershipChanged(granted, contract.Membership{
		Org:       org,
		Account:   m.account,
		AccountID: contract.AccountID(m.cluster, m.account),
		Role:      m.role,
		User:      contract.User{ID: m.user},
	}, key, c.Timestamp)
	if err != nil {
		return err
	}
	if err := a.Publish(ctx, e); err != nil {
		return err
	}
	a.Log.Info().Str("org", org).Str("type", e.Type()).Str("id", e.ID()).Msg("published")
	return nil
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
			a.Log.Error().Err(err).Str("org", org).Msg("ignoring broken state")
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
