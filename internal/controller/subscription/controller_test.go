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
)

type fakeBackend struct {
	ensured, deleted []string
}

func (f *fakeBackend) Ensure(_ context.Context, name string, _ *v1alpha1.Subscription) (delivery.Status, error) {
	f.ensured = append(f.ensured, name)
	return delivery.Status{Ready: true, Reason: "Ready"}, nil
}

func (f *fakeBackend) Delete(_ context.Context, name string) error {
	f.deleted = append(f.deleted, name)
	return nil
}

func workspace(t *testing.T, objs ...ctrlruntimeclient.Object) ctrlruntimeclient.Client {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&v1alpha1.Subscription{}).
		Build()
}

func subscription() *v1alpha1.Subscription {
	return &v1alpha1.Subscription{ObjectMeta: metav1.ObjectMeta{Name: "s"}}
}

func TestReconcile(t *testing.T) {
	backend := &fakeBackend{}
	c := workspace(t, subscription())
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)

	got := &v1alpha1.Subscription{}
	require.NoError(t, c.Get(context.Background(), types.NamespacedName{Name: "s"}, got))
	assert.Equal(t, metav1.ConditionTrue, got.Status.Conditions[0].Status)
	assert.Contains(t, got.Finalizers, finalizer)
	assert.Equal(t, []string{deliveryName("c1", "s")}, backend.ensured)
}

func TestDelete(t *testing.T) {
	backend := &fakeBackend{}
	sub := subscription()
	sub.Finalizers = []string{finalizer}
	sub.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	c := workspace(t, sub)
	r := &Reconciler{Delivery: backend}

	_, err := r.reconcile(context.Background(), c, "c1", types.NamespacedName{Name: "s"})
	require.NoError(t, err)
	assert.Equal(t, []string{deliveryName("c1", "s")}, backend.deleted)
}
