/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package subscription reconciles Subscriptions from all organization
// workspaces that bound the eventing API into the delivery backend.
package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery/knative"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const finalizer = "eventing.platform-mesh.io/delivery"

// Delivery is what the controller needs from a delivery backend. Knative is the
// only one for now.
type Delivery interface {
	Ensure(ctx context.Context, org, trigger string, sub *v1alpha1.Subscription) (knative.Status, error)
	Delete(ctx context.Context, org, trigger string) error
}

type Reconciler struct {
	Manager  mcmanager.Manager
	Delivery Delivery
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	return mcbuilder.ControllerManagedBy(mgr).
		Named("subscription").
		For(&v1alpha1.Subscription{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	c := cl.GetClient()

	sub := &v1alpha1.Subscription{}
	if err := c.Get(ctx, req.NamespacedName, sub); err != nil {
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	trigger := triggerName(string(req.ClusterName), sub.Name)

	if !sub.DeletionTimestamp.IsZero() {
		if sub.Status.Org != "" {
			if err := r.Delivery.Delete(ctx, sub.Status.Org, trigger); err != nil {
				return ctrl.Result{}, err
			}
		}
		controllerutil.RemoveFinalizer(sub, finalizer)
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(c.Update(ctx, sub))
	}
	if controllerutil.AddFinalizer(sub, finalizer) {
		if err := c.Update(ctx, sub); err != nil {
			return ctrl.Result{}, err
		}
	}

	// The workspace path is only on the LogicalCluster object, which we can read
	// because of the logicalclusters permission claim on the APIExport.
	lc := &kcpcorev1alpha1.LogicalCluster{}
	if err := c.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "cluster"}, lc); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			return r.setStatus(ctx, c, sub, "", knative.Status{
				Reason:  "ClaimNotAccepted",
				Message: "accept the logicalclusters permission claim on the APIBinding",
			}, 0)
		}
		return ctrl.Result{}, err
	}
	path := lc.Annotations["kcp.io/path"]
	org, ok := orgs.FromPath(path)
	if !ok {
		return r.setStatus(ctx, c, sub, "", knative.Status{
			Reason:  "NotAnOrgWorkspace",
			Message: fmt.Sprintf("Subscriptions only work in root:orgs:<org> workspaces, not in %s", path),
		}, 0)
	}

	status, err := r.Delivery.Ensure(ctx, org, trigger, sub)
	if err != nil {
		return ctrl.Result{}, err
	}
	// We don't watch Triggers, so check again until delivery is ready.
	requeue := time.Duration(0)
	if !status.Ready {
		requeue = 5 * time.Second
	}
	return r.setStatus(ctx, c, sub, org, status, requeue)
}

func (r *Reconciler) setStatus(ctx context.Context, c ctrlruntimeclient.Client, sub *v1alpha1.Subscription, org string, s knative.Status, requeue time.Duration) (ctrl.Result, error) {
	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             s.Reason,
		Message:            s.Message,
		ObservedGeneration: sub.Generation,
	}
	if s.Ready {
		cond.Status = metav1.ConditionTrue
	}
	sub.Status.Org = org
	sub.Status.ObservedGeneration = sub.Generation
	meta.SetStatusCondition(&sub.Status.Conditions, cond)
	if err := c.Status().Update(ctx, sub); err != nil {
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// triggerName is stable for a Subscription and unique across workspaces.
func triggerName(cluster, name string) string {
	sum := sha256.Sum256([]byte(cluster + "/" + name))
	return "sub-" + hex.EncodeToString(sum[:])[:16]
}
