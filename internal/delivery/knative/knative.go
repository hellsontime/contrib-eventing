/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package knative delivers events with Knative Eventing: one Broker per
// organization, one Trigger per Subscription. Knative objects are handled as
// unstructured so we don't depend on Knative's Go modules.
package knative

import (
	"context"
	"fmt"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const brokerName = "default"

var (
	brokerGVK  = schema.GroupVersionKind{Group: "eventing.knative.dev", Version: "v1", Kind: "Broker"}
	triggerGVK = schema.GroupVersionKind{Group: "eventing.knative.dev", Version: "v1", Kind: "Trigger"}
)

// Backend implements delivery.Backend.
type Backend struct {
	Client ctrlruntimeclient.Client
}

// Ensure makes sure the organization has a Broker and the Subscription has a
// Trigger, and reports whether the Trigger is ready.
func (b *Backend) Ensure(ctx context.Context, org, trigger string, sub *v1alpha1.Subscription) (delivery.Status, error) {
	ns := orgs.Namespace(org)
	if err := b.ensureBroker(ctx, org, ns); err != nil {
		return delivery.Status{}, err
	}

	t := &unstructured.Unstructured{}
	t.SetGroupVersionKind(triggerGVK)
	t.SetNamespace(ns)
	t.SetName(trigger)
	_, err := controllerutil.CreateOrUpdate(ctx, b.Client, t, func() error {
		t.SetAnnotations(map[string]string{"eventing.platform-mesh.io/subscription": sub.Name})
		return unstructured.SetNestedMap(t.Object, triggerSpec(sub), "spec")
	})
	if err != nil {
		return delivery.Status{}, fmt.Errorf("trigger %s/%s: %w", ns, trigger, err)
	}
	return readiness(t), nil
}

// Delete removes the Subscription's Trigger. The Broker stays.
func (b *Backend) Delete(ctx context.Context, org, trigger string) error {
	t := &unstructured.Unstructured{}
	t.SetGroupVersionKind(triggerGVK)
	t.SetNamespace(orgs.Namespace(org))
	t.SetName(trigger)
	return ctrlruntimeclient.IgnoreNotFound(b.Client.Delete(ctx, t))
}

func (b *Backend) ensureBroker(ctx context.Context, org, ns string) error {
	n := &corev1.Namespace{}
	n.Name = ns
	if _, err := controllerutil.CreateOrUpdate(ctx, b.Client, n, func() error {
		if n.Labels == nil {
			n.Labels = map[string]string{}
		}
		n.Labels[orgs.Label] = org
		return nil
	}); err != nil {
		return fmt.Errorf("namespace %s: %w", ns, err)
	}

	br := &unstructured.Unstructured{}
	br.SetGroupVersionKind(brokerGVK)
	br.SetNamespace(ns)
	br.SetName(brokerName)
	if err := b.Client.Create(ctx, br); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("broker %s/%s: %w", ns, brokerName, err)
	}
	return nil
}

// triggerSpec translates a Subscription into a Trigger spec. The filter keeps
// events whose type is any of the listed types.
func triggerSpec(sub *v1alpha1.Subscription) map[string]any {
	anyOf := make([]any, 0, len(sub.Spec.Filter.Types))
	for _, t := range sub.Spec.Filter.Types {
		anyOf = append(anyOf, map[string]any{"exact": map[string]any{"type": t}})
	}
	spec := map[string]any{
		"broker":     brokerName,
		"filters":    []any{map[string]any{"any": anyOf}},
		"subscriber": map[string]any{"uri": sub.Spec.Sink.URI},
	}
	if d := sub.Spec.Delivery; d != nil {
		delivery := map[string]any{}
		if d.Retry != nil {
			delivery["retry"] = int64(*d.Retry)
		}
		if d.BackoffPolicy != nil {
			delivery["backoffPolicy"] = *d.BackoffPolicy
		}
		if d.BackoffDelay != nil {
			delivery["backoffDelay"] = *d.BackoffDelay
		}
		spec["delivery"] = delivery
	}
	return spec
}

// readiness reads the Trigger's Ready condition. Knative folds the Broker's
// state into it, so this is the only thing we need to look at.
func readiness(t *unstructured.Unstructured) delivery.Status {
	conditions, _, _ := unstructured.NestedSlice(t.Object, "status", "conditions")
	for _, c := range conditions {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != "Ready" {
			continue
		}
		reason, _ := m["reason"].(string)
		message, _ := m["message"].(string)
		if m["status"] == "True" {
			return delivery.Status{Ready: true, Reason: "Ready", Message: "events are delivered to the sink"}
		}
		if reason == "" {
			reason = "TriggerNotReady"
		}
		return delivery.Status{Reason: reason, Message: message}
	}
	return delivery.Status{Reason: "TriggerNotReady", Message: "waiting for Knative to set up delivery"}
}
