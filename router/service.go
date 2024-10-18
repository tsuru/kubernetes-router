// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"context"
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

	// Domain suffix is used to append at name of app, ie: myapp.<domainSuffix>
	DomainSuffix = "domain-suffix"

	// Domain prefix is used to prepend at name of app, ie: <domainPrefix>.myapp.<domainSuffix>
	DomainPrefix = "domain-prefix"
	// Route is the route option name
	Route = "route"

	// Acme is the acme option name
	Acme = "tls-acme"

	// AcmeCName is the acme option for cnames
	AcmeCName = "tls-acme-cname"

	ExternalTrafficPolicy = "external-traffic-policy"

	// optsAnnotation is the name of the annotation used to store opts.
	optsAnnotation = "router.tsuru.io/opts"

	AllPrefixes = "all-prefixes"
)

// ErrIngressAlreadyExists is the error returned by the service when
// trying to create a service that already exists
var ErrIngressAlreadyExists = errors.New("ingress already exists")

type InstanceID struct {
	InstanceName string
	AppName      string
}

type BackendStatus string

var (
	BackendStatusReady    = BackendStatus("ready")
	BackendStatusNotReady = BackendStatus("not ready")
)

// Router implements the basic functionally needed to
// ingresses and/or loadbalancers.
type Router interface {
	Ensure(ctx context.Context, id InstanceID, o EnsureBackendOpts) error
	Remove(ctx context.Context, id InstanceID) error
	GetAddresses(ctx context.Context, id InstanceID) ([]string, error)
	SupportedOptions(ctx context.Context) map[string]string
}

// RouterStatus could report status of backend
type RouterStatus interface {
	Router
	GetStatus(ctx context.Context, id InstanceID) (status BackendStatus, detail string, err error)
}

// RouterTLS Certificates interface
type RouterTLS interface {
	Router
	AddCertificate(ctx context.Context, id InstanceID, certName string, cert CertData) error
	GetCertificate(ctx context.Context, id InstanceID, certName string) (*CertData, error)
	RemoveCertificate(ctx context.Context, id InstanceID, certName string) error
}

// Opts used when creating/updating routers
type Opts struct {
	Pool                  string            `json:",omitempty"`
	ExposedPort           string            `json:",omitempty"`
	Domain                string            `json:",omitempty"`
	Route                 string            `json:",omitempty"`
	DomainSuffix          string            `json:",omitempty"`
	DomainPrefix          string            `json:",omitempty"`
	ExternalTrafficPolicy string            `json:",omitempty"`
	AdditionalOpts        map[string]string `json:",omitempty"`
	HeaderOpts            []string          `json:",omitempty"`
	Acme                  bool              `json:",omitempty"`
	AcmeCName             bool              `json:",omitempty"`
	ExposeAllServices     bool              `json:",omitempty"`
}

// CertData user when adding certificates
type CertData struct {
	Certificate string `json:"certificate"`
	Key         string `json:"key"`
}

type BackendPrefix struct {
	Prefix string        `json:"prefix"`
	Target BackendTarget `json:"target"`
}

type EnsureBackendOpts struct {
	Opts        Opts              `json:"opts"`
	CNames      []string          `json:"cnames"`
	Team        string            `json:"team"`
	Tags        []string          `json:"tags,omitempty"`
	CertIssuers map[string]string `json:"certIssuers"`
	Prefixes    []BackendPrefix   `json:"prefixes"`
}

type BackendTarget struct {
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
		case DomainSuffix:
			o.DomainSuffix = strV
		case DomainPrefix:
			o.DomainPrefix = strV
		case Route:
			o.Route = strV
		case ExternalTrafficPolicy:
			o.ExternalTrafficPolicy = strV
		case Acme:
			o.Acme, err = strconv.ParseBool(strV)
			if err != nil {
				o.Acme = false
			}
		case AcmeCName:
			o.AcmeCName, err = strconv.ParseBool(strV)
			if err != nil {
				o.AcmeCName = false
			}
		case AllPrefixes:
			o.ExposeAllServices, err = strconv.ParseBool(strV)
			if err != nil {
				o.ExposeAllServices = false
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
		AcmeCName:   "If set to true, adds ingress TLS options to CName Ingresses. Defaults to false.",
		AllPrefixes: "If set to true, exposes all of the services of the app, allowing them to be accessible from the router.",
	}
}

// HealthcheckableRouter is a Service that implements
// a way to check of its health
type HealthcheckableRouter interface {
	Healthcheck() error
}
