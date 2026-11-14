/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package kcpsetup puts the eventing API into the provider workspace: the
// APIResourceSchema, the APIExport and an APIBinding of the export in the same
// workspace, so platform admins can create Subscriptions there.
package kcpsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"time"

	provider "github.com/platform-mesh/contrib-eventing/config/kcp/provider"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ExportName = "eventing.platform-mesh.io"
	FieldOwner = "eventing-kcp-setup"
)

// Objects returns what Apply applies, in order: the schemas, the export, the binding.
func Objects() ([]*unstructured.Unstructured, error) {
	files, err := fs.Glob(provider.Manifests, "*.yaml")
	if err != nil {
		return nil, err
	}
	var schemas, exports []*unstructured.Unstructured
	for _, f := range files {
		data, err := provider.Manifests.ReadFile(f)
		if err != nil {
			return nil, err
		}
		objs, err := decode(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for _, o := range objs {
			switch o.GetKind() {
			case "APIResourceSchema":
				schemas = append(schemas, o)
			case "APIExport":
				exports = append(exports, o)
			default:
				return nil, fmt.Errorf("%s: unexpected kind %s", f, o.GetKind())
			}
		}
	}
	if len(exports) != 1 || exports[0].GetName() != ExportName {
		return nil, fmt.Errorf("want exactly the APIExport %s in the manifests", ExportName)
	}
	if err := checkSchemas(exports[0], schemas); err != nil {
		return nil, err
	}

	// No path: the export is in the binding's own workspace.
	binding := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIBinding",
		"metadata":   map[string]any{"name": ExportName},
		"spec": map[string]any{
			"reference": map[string]any{"export": map[string]any{"name": ExportName}},
		},
	}}
	return append(append(schemas, exports...), binding), nil
}

// checkSchemas makes sure the export names only schemas that are applied with it.
func checkSchemas(export *unstructured.Unstructured, schemas []*unstructured.Unstructured) error {
	have := map[string]bool{}
	for _, s := range schemas {
		have[s.GetName()] = true
	}
	resources, _, err := unstructured.NestedSlice(export.Object, "spec", "resources")
	if err != nil {
		return err
	}
	if len(resources) == 0 {
		return errors.New("the APIExport has no resources")
	}
	for _, r := range resources {
		name, _, _ := unstructured.NestedString(r.(map[string]any), "schema")
		if !have[name] {
			return fmt.Errorf("the APIExport names schema %q, which is not in the manifests", name)
		}
	}
	return nil
}

func decode(data []byte) ([]*unstructured.Unstructured, error) {
	var objs []*unstructured.Unstructured
	d := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		o := &unstructured.Unstructured{}
		if err := d.Decode(&o.Object); err != nil {
			if errors.Is(err, io.EOF) {
				return objs, nil
			}
			return nil, err
		}
		if len(o.Object) > 0 {
			objs = append(objs, o)
		}
	}
}

// Apply server-side applies the objects, then waits until the binding is Ready
// and kcp has created the export's endpoint slice, which the controller watches
// through. Schemas are immutable: applying one with a known name but a
// different spec fails.
func Apply(ctx context.Context, c ctrlruntimeclient.Client, objs []*unstructured.Unstructured, poll time.Duration) error {
	for _, o := range objs {
		if err := c.Apply(ctx, ctrlruntimeclient.ApplyConfigurationFromUnstructured(o.DeepCopy()),
			ctrlruntimeclient.FieldOwner(FieldOwner), ctrlruntimeclient.ForceOwnership); err != nil {
			return fmt.Errorf("apply %s %s: %w", o.GetKind(), o.GetName(), err)
		}
	}

	var reason string
	err := wait.PollUntilContextCancel(ctx, poll, true, func(ctx context.Context) (bool, error) {
		b := &unstructured.Unstructured{}
		b.SetAPIVersion("apis.kcp.io/v1alpha2")
		b.SetKind("APIBinding")
		if err := c.Get(ctx, ctrlruntimeclient.ObjectKey{Name: ExportName}, b); err != nil {
			return false, err
		}
		ready, why := readyCondition(b)
		reason = "APIBinding " + ExportName + " not Ready: " + why
		return ready, nil
	})
	if err != nil {
		return fmt.Errorf("%s: %w", reason, err)
	}

	err = wait.PollUntilContextCancel(ctx, poll, true, func(ctx context.Context) (bool, error) {
		s := &unstructured.Unstructured{}
		s.SetAPIVersion("apis.kcp.io/v1alpha1")
		s.SetKind("APIExportEndpointSlice")
		err := c.Get(ctx, ctrlruntimeclient.ObjectKey{Name: ExportName}, s)
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	})
	if err != nil {
		return fmt.Errorf("APIExportEndpointSlice %s: %w", ExportName, err)
	}
	return nil
}

func readyCondition(o *unstructured.Unstructured) (bool, string) {
	conds, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != "Ready" {
			continue
		}
		if m["status"] == "True" {
			return true, ""
		}
		return false, fmt.Sprintf("%v: %v", m["reason"], m["message"])
	}
	return false, "no Ready condition yet"
}
