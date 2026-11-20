/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package kcpsetup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestObjects(t *testing.T) {
	objs, err := Objects()
	require.NoError(t, err)

	kinds := make([]string, 0, len(objs))
	for _, o := range objs {
		kinds = append(kinds, o.GetKind())
	}
	assert.Equal(t, []string{"APIResourceSchema", "APIExport", "APIBinding"}, kinds)

	binding := objs[len(objs)-1]
	assert.Equal(t, ExportName, binding.GetName())
	ref, _, _ := unstructured.NestedMap(binding.Object, "spec", "reference", "export")
	assert.Equal(t, map[string]any{"name": ExportName}, ref, "no path, so the export of the binding's own workspace")
}

func TestCheckSchemas(t *testing.T) {
	export := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"resources": []any{map[string]any{"schema": "v1.subscriptions.eventing.platform-mesh.io"}},
	}}}
	schema := &unstructured.Unstructured{}
	schema.SetName("v1.subscriptions.eventing.platform-mesh.io")
	assert.NoError(t, checkSchemas(export, []*unstructured.Unstructured{schema}))

	schema.SetName("v0.subscriptions.eventing.platform-mesh.io")
	assert.ErrorContains(t, checkSchemas(export, []*unstructured.Unstructured{schema}), `names schema "v1.subscriptions`)
}

// fakeKCP stands in for kcp: it takes every apply and sets the binding's
// status the way kcp would.
func fakeKCP(t *testing.T, bindingStatus map[string]any, slice bool) ctrlruntimeclient.Client {
	t.Helper()
	applied := map[string]bool{}
	return fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
		Apply: func(ctx context.Context, c ctrlruntimeclient.WithWatch, obj runtime.ApplyConfiguration, opts ...ctrlruntimeclient.ApplyOption) error {
			applied[obj.(interface{ GetKind() string }).GetKind()] = true
			return nil
		},
		Get: func(ctx context.Context, c ctrlruntimeclient.WithWatch, key ctrlruntimeclient.ObjectKey, obj ctrlruntimeclient.Object, opts ...ctrlruntimeclient.GetOption) error {
			u := obj.(*unstructured.Unstructured)
			switch u.GetKind() {
			case "APIBinding":
				require.True(t, applied["APIBinding"], "binding read before it was applied")
				u.Object["status"] = bindingStatus
				return nil
			case "APIExportEndpointSlice":
				if slice {
					return nil
				}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	}).Build()
}

func TestApply(t *testing.T) {
	objs, err := Objects()
	require.NoError(t, err)
	ready := map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}

	require.NoError(t, Apply(context.Background(), fakeKCP(t, ready, true), objs, time.Millisecond))
}

func TestApplyNotReady(t *testing.T) {
	objs, err := Objects()
	require.NoError(t, err)
	notReady := map[string]any{"conditions": []any{map[string]any{
		"type": "Ready", "status": "False", "reason": "APIExportNotFound", "message": "no export",
	}}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = Apply(ctx, fakeKCP(t, notReady, true), objs, time.Millisecond)
	assert.ErrorContains(t, err, "APIBinding eventing.platform-mesh.io not Ready: APIExportNotFound: no export")
}

func TestApplyNoEndpointSlice(t *testing.T) {
	objs, err := Objects()
	require.NoError(t, err)
	ready := map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	assert.ErrorContains(t, Apply(ctx, fakeKCP(t, ready, false), objs, time.Millisecond), "APIExportEndpointSlice eventing.platform-mesh.io")
}
