/*
Copyright The Platform Mesh Authors.
SPDX-License-Identifier: Apache-2.0
*/

// Package publish posts CloudEvents over HTTP.
package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/binding"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"
)

// Publisher posts events in CloudEvents binary mode. If TokenFile is set, the
// token in it is sent as bearer token. The file is read on every call, because
// projected ServiceAccount tokens are rotated.
type Publisher struct {
	Client    *http.Client
	TokenFile string
}

func (p *Publisher) Publish(ctx context.Context, url string, e cloudevents.Event) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	if err := cehttp.WriteRequest(ctx, binding.ToMessage(&e), req); err != nil {
		return err
	}
	if p.TokenFile != "" {
		token, err := os.ReadFile(p.TokenFile)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("POST %s: %s: %s", url, resp.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}
