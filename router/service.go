// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"errors"
)

// ErrIngressAlreadyExists is the error returned by the service when
// trying to create a service that already exists
var ErrIngressAlreadyExists = errors.New("ingress already exists")

// Service implements the basic functionally needed to
// manage ingresses.
type Service interface {
	Create(appName string, opts Opts) error
	Remove(appName string) error
	Update(appName string, opts Opts) error
	Swap(appSrc, appDst string) error
	Get(appName string) (map[string]string, error)
	Addresses(appName string) ([]string, error)
}

// Opts used when creating/updating routers
type Opts struct {
	Pool           string
	ExposedPort    string
	AdditionalOpts map[string]string
}

func (o *Opts) UnmarshalJSON(bs []byte) (err error) {
	m := make(map[string]interface{})

	if err = json.Unmarshal(bs, &m); err != nil {
		return err
	}

	if o.AdditionalOpts == nil {
		o.AdditionalOpts = make(map[string]string)
	}

	for k, v := range m {
		strV, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "tsuru.io/app-pool":
			o.Pool = strV
		case "exposed-port":
			o.ExposedPort = strV
		default:
			o.AdditionalOpts[k] = strV
		}
	}

	return err
}

// HealthcheckableService is a Service that implements
// a way to check of its health
type HealthcheckableService interface {
	Healthcheck() error
}
