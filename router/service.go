// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ExposedPort is the exposed port option name
	ExposedPort = "exposed-port"

	// Domain is the domain option name
	Domain = "domain"

	// Route is the route option name
	Route = "route"

	// Acme is the acme option name
	Acme = "tls-acme"

	// optsAnnotation is the name of the annotation used to store opts.
	optsAnnotation = "router.tsuru.io/opts"
)

// ErrIngressAlreadyExists is the error returned by the service when
// trying to create a service that already exists
var ErrIngressAlreadyExists = errors.New("ingress already exists")

// Service implements the basic functionally needed to
// manage ingresses.
type Service interface {
	Create(appName string, opts Opts) error
	Remove(appName string) error
	Update(appName string, extraData RoutesRequestExtraData) error
	Swap(appSrc, appDst string) error
	GetAddresses(appName string) ([]string, error)
	SupportedOptions() map[string]string
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
	Pool           string            `json:",omitempty"`
	ExposedPort    string            `json:",omitempty"`
	Domain         string            `json:",omitempty"`
	Route          string            `json:",omitempty"`
	Acme           bool              `json:",omitempty"`
	AdditionalOpts map[string]string `json:",omitempty"`
	HeaderOpts     []string          `json:",omitempty"`
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

type RoutesRequestData struct {
	Prefix    string                 `json:"prefix"`
	ExtraData RoutesRequestExtraData `json:"extraData"`
}

type RoutesRequestExtraData struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
}

func (o *Opts) ToAnnotations() (map[string]string, error) {
	data, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		optsAnnotation: string(data),
	}, nil
}

func OptsFromAnnotations(meta *metav1.ObjectMeta) (Opts, error) {
	if meta.Annotations == nil || meta.Annotations[optsAnnotation] == "" {
		return Opts{}, nil
	}
	type rawJsonOpts Opts
	var o rawJsonOpts
	err := json.Unmarshal([]byte(meta.Annotations[optsAnnotation]), &o)
	return Opts(o), err
}

// UnmarshalJSON unmarshals Opts from a byte array parsing known fields
// and adding all other string fields to AdditionalOpts
func (o *Opts) UnmarshalJSON(bs []byte) (err error) {
	m := make(map[string]interface{})

	if err = json.Unmarshal(bs, &m); err != nil {
		return err
	}

	for _, headerOpt := range o.HeaderOpts {
		parts := strings.SplitN(headerOpt, "=", 2)
		if len(parts) == 0 {
			continue
		}
		key := parts[0]
		if _, ok := m[key]; ok {
			continue
		}
		var value string
		if len(parts) > 1 {
			value = parts[1]
		}
		m[key] = value
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
