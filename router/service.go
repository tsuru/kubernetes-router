// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"errors"
	"strconv"
)

const (
	ExposedPort = "exposed-port"
	Domain      = "domain"
	Route       = "route"
	Acme        = "tls-acme"
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
	SupportedOptions() (map[string]string, error)
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
	Acme           bool
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
		case ExposedPort:
			o.ExposedPort = strV
		case Domain:
			o.Domain = strV
		case Route:
			o.Route = strV
		case Acme:
			o.Acme, err = strconv.ParseBool(strV)
			if err != nil {
				o.Acme = false
			}
		default:
			o.AdditionalOpts[k] = strV
		}
	}

	return err
}

// DescribedOptions returns a map containing all the available options
// and their description as values of the map
func DescribedOptions() map[string]string {
	return map[string]string{
		ExposedPort: "Port to be exposed by the Load Balancer. Defaults to 80.",
		Domain:      "Domain used on Ingress.",
		Route:       "Path used on Ingress rule.",
		Acme:        "If set to true, adds ingress TLS options to Ingress. Defaults to false.",
	}
}

// HealthcheckableService is a Service that implements
// a way to check of its health
type HealthcheckableService interface {
	Healthcheck() error
}
