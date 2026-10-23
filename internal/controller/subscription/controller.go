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
	"github.com/platform-mesh/contrib-eventing/internal/delivery"
	"github.com/platform-mesh/contrib-eventing/internal/orgs"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

const finalizer = "eventing.platform-mesh.io/delivery"

type Reconciler struct {
	Manager  mcmanager.Manager
	Delivery delivery.Backend
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
	return r.reconcile(ctx, cl.GetClient(), string(req.ClusterName), req.NamespacedName)
}

// reconcile works on one workspace, through a client for that workspace.
func (r *Reconciler) reconcile(ctx context.Context, c ctrlruntimeclient.Client, cluster string, key types.NamespacedName) (ctrl.Result, error) {
	sub := &v1alpha1.Subscription{}
	if err := c.Get(ctx, key, sub); err != nil {
		return ctrl.Result{}, ctrlruntimeclient.IgnoreNotFound(err)
	}
	name := deliveryName(cluster, sub.Name)
	org, problem, err := workspaceOrg(ctx, c)
	if err != nil {
		return ctrl.Result{}, err
	}

	if !sub.DeletionTimestamp.IsZero() {
		// The status may not have been written yet when delivery was already set
		// up, so the workspace path decides, and the status is only a fallback.
		if org == "" {
			org = sub.Status.Org
		}
		if org != "" {
			if err := r.Delivery.Delete(ctx, org, name); err != nil {
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
	if problem != nil {
		return setStatus(ctx, c, sub, "", *problem, 0)
	}

	status, err := r.Delivery.Ensure(ctx, org, name, sub)
	if err != nil {
		return ctrl.Result{}, err
	}
	// We don't watch the backend, so check again until delivery is ready.
	requeue := time.Duration(0)
	if !status.Ready {
		requeue = 5 * time.Second
	}
	return setStatus(ctx, c, sub, org, status, requeue)
}

// workspaceOrg returns the org of the workspace. The path is only on the
// LogicalCluster object, which we can read because of the logicalclusters
// permission claim on the APIExport. If the workspace is not an org, it
// returns the status the Subscription should show instead.
func workspaceOrg(ctx context.Context, c ctrlruntimeclient.Client) (string, *delivery.Status, error) {
	lc := &kcpcorev1alpha1.LogicalCluster{}
	if err := c.Get(ctx, ctrlruntimeclient.ObjectKey{Name: "cluster"}, lc); err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsNotFound(err) {
			return "", &delivery.Status{
				Reason:  "ClaimNotAccepted",
				Message: "accept the logicalclusters permission claim on the APIBinding",
			}, nil
		}
		return "", nil, err
	}
	path := lc.Annotations["kcp.io/path"]
	org, ok := orgs.FromPath(path)
	if !ok {
		return "", &delivery.Status{
			Reason:  "NotAnOrgWorkspace",
			Message: fmt.Sprintf("Subscriptions only work in root:orgs:<org> workspaces, not in %s", path),
		}, nil
	}
	return org, nil, nil
}

func setStatus(ctx context.Context, c ctrlruntimeclient.Client, sub *v1alpha1.Subscription, org string, s delivery.Status, requeue time.Duration) (ctrl.Result, error) {
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

// deliveryName is stable for a Subscription and unique across workspaces.
func deliveryName(cluster, name string) string {
	sum := sha256.Sum256([]byte(cluster + "/" + name))
	return "sub-" + hex.EncodeToString(sum[:])[:16]
}
