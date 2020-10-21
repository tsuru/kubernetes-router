// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backend

import (
	"context"
	"errors"
	"net/http"

	"github.com/tsuru/kubernetes-router/router"
)

var (
	ErrBackendNotFound = errors.New("Backend not found")
)

type Backend interface {
	Router(ctx context.Context, mode string, header http.Header) (router.Router, error)
	Healthcheck(ctx context.Context) error
}
