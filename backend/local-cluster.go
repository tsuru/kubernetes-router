// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
)

var _ Backend = &LocalCluster{}

type LocalCluster struct {
	DefaultMode string
	Routers     map[string]router.Router
}

func (m *LocalCluster) Router(ctx context.Context, mode string, _ http.Header) (router.Router, error) {
	if mode == "" {
		mode = m.DefaultMode
	}
	svc, ok := m.Routers[mode]
	if !ok {
		return nil, ErrBackendNotFound
	}
	return svc, nil
}

func (m *LocalCluster) Healthcheck(ctx context.Context) error {
	errAccumulator := &multiRoutersErrors{}

	for mode, svc := range m.Routers {
		if hc, ok := svc.(router.HealthcheckableRouter); ok {
			if err := hc.Healthcheck(); err != nil {
				errAccumulator.errors = append(errAccumulator.errors, fmt.Sprintf("failed to check IngressService %v: %v", mode, err))
			}
		}
	}

	if len(errAccumulator.errors) > 0 {
		return errAccumulator
	}

	return nil
}

type multiRoutersErrors struct {
	errors []string
}

func (m *multiRoutersErrors) Error() string {
	return strings.Join(m.errors, " - ")
}
