/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package subscription

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
	"github.com/platform-mesh/contrib-eventing/internal/delivery"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kcpcorev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"
)

type fakeBackend struct {
	ensured, deleted []string
}

func (f *fakeBackend) Ensure(_ context.Context, org, name string, _ *v1alpha1.Subscription) (delivery.Status, error) {
	f.ensured = append(f.ensured, org+"/"+name)
	return delivery.Status{Ready: true, Reason: "Ready"}, nil
}

func (f *fakeBackend) Delete(_ context.Context, org, name string) error {
	f.deleted = append(f.deleted, org+"/"+name)
	return nil
}

// workspace returns a client for a workspace with the given path and objects.
func workspace(t *testing.T, path string, objs ...ctrlruntimeclient.Object) ctrlruntimeclient.Client {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, kcpcorev1alpha1.AddToScheme(scheme))
	lc := &kcpcorev1alpha1.LogicalCluster{ObjectMeta: metav1.ObjectMeta{
		Name: "cluster", Annotations: map[string]string{"kcp.io/path": path},
	}}
	return fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(append(objs, lc)...).
		WithStatusSubresource(&v1alpha1.Subscription{}).
		Build()
}

func subscription() *v1alpha1.Subscription {
	return &v1alpha1.Subscription{ObjectMeta: metav1.ObjectMeta{Name: "s"}}
}

func TestReconcileInOrgWorkspace(t *testing.T) {
	backend := &fakeBackend{}
	c := workspace(t, "root:orgs:acme", subscription())
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)

	got := &v1alpha1.Subscription{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
	assert.Equal(t, "acme", got.Status.Org)
	assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
	assert.Contains(t, got.Finalizers, finalizer)
	assert.Equal(t, []string{"acme/" + deliveryName("c1", "s")}, backend.ensured)
}

func TestReconcileOutsideOrgWorkspace(t *testing.T) {
	backend := &fakeBackend{}
	c := workspace(t, "root:orgs:acme:team-a", subscription())
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)

	got := &v1alpha1.Subscription{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
	assert.Equal(t, "NotAnOrgWorkspace", got.Status.Conditions[0].Reason)
	assert.Empty(t, backend.ensured)
}

// Delivery can be set up before the status with the org is written. Deleting
// the Subscription must still remove it.
func TestDeleteWithoutOrgInStatus(t *testing.T) {
	backend := &fakeBackend{}
	sub := subscription()
	sub.Finalizers = []string{finalizer}
	sub.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	c := workspace(t, "root:orgs:acme", sub)
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)
	assert.Equal(t, []string{"acme/" + deliveryName("c1", "s")}, backend.deleted)
}
