/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

package knative

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/platform-mesh/contrib-eventing/api/v1alpha1"
)

func TestTriggerSpec(t *testing.T) {
	retry := int32(3)
	sub := &v1alpha1.Subscription{Spec: v1alpha1.SubscriptionSpec{
		Filter:   v1alpha1.Filter{Types: []string{"a", "b"}},
		Sink:     v1alpha1.Sink{URI: "http://sink"},
		Delivery: &v1alpha1.Delivery{Retry: &retry},
	}}
	assert.Equal(t, map[string]any{
		"broker": "default",
		"filters": []any{map[string]any{"any": []any{
			map[string]any{"exact": map[string]any{"type": "a"}},
			map[string]any{"exact": map[string]any{"type": "b"}},
		}}},
		"subscriber": map[string]any{"uri": "http://sink"},
		"delivery":   map[string]any{"retry": int64(3)},
	}, triggerSpec(sub))
}
