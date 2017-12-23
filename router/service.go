// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"errors"
	"strconv"
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

// ServiceTLS Certificates interface
type ServiceTLS interface {
	Service
	AddCertificate(appName string, certName string, cert CertData) error
	GetCertificate(appName string, certName string) (*CertData, error)
	RemoveCertificate(appName string, certName string) error
}

// ServiceCNAME Certificates interface
type ServiceCNAME interface {
	Service
	SetCname(appName string, cname string) error
	GetCnames(appName string) (*CnamesResp, error)
	UnsetCname(appName string, cname string) error
}

// Opts used when creating/updating routers
type Opts struct {
	Pool           string
	ExposedPort    string
	Domain         string
	Route          string
	KubeLego       bool
	AdditionalOpts map[string]string
}

// CnamesResp used when adding cnames
type CnamesResp struct {
	Cnames []string `json:"cnames"`
}

// CertData user when adding certificates
type CertData struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

// UnmarshalJSON unmarshals Opts from a byte array parsing known fields
// and adding all other string fields to AdditionalOpts
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
		case "domain":
			o.Domain = strV
		case "route":
			o.Route = strV
		case "kubelego":
			o.KubeLego, err = strconv.ParseBool(strV)
			if err != nil {
				o.KubeLego = false
			}
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
