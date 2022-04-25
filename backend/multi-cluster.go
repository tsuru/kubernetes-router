// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package backend

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/tsuru/kubernetes-router/kubernetes"
	"github.com/tsuru/kubernetes-router/observability"
	"github.com/tsuru/kubernetes-router/router"
	kubernetesGO "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var _ Backend = &MultiCluster{}

type ClusterConfig struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Address string `json:"address"`
	Token   string `json:"token"`

	AuthProvider string `json:"authProvider"`
}

type ClustersFile struct {
	Clusters []ClusterConfig `json:"clusters"`
}

type MultiCluster struct {
	Namespace  string
	Fallback   Backend
	K8sTimeout *time.Duration
	Modes      []string
	Clusters   []ClusterConfig
}

func (m *MultiCluster) Router(ctx context.Context, mode string, headers http.Header) (router.Router, error) {
	name := headers.Get("X-Tsuru-Cluster-Name")
	address := headers.Get("X-Tsuru-Cluster-Addresses")

	if address == "" {
		return m.Fallback.Router(ctx, mode, headers)
	}

	span := opentracing.SpanFromContext(ctx)
	if span != nil {
		span.SetTag("cluster.name", name)
		span.SetTag("cluster.address", address)
	}

	timeout := time.Second * 10
	if m.K8sTimeout != nil {
		timeout = *m.K8sTimeout
	}

	kubernetesRestConfig, err := m.getKubeConfig(name, address, timeout)
	if err != nil {
		return nil, err
	}

	k8sClient, err := kubernetesGO.NewForConfig(kubernetesRestConfig)
	if err != nil {
		return nil, err
	}

	baseService := &kubernetes.BaseService{
		Namespace:  m.Namespace,
		Timeout:    timeout,
		Client:     k8sClient,
		RestConfig: kubernetesRestConfig,
	}

	if mode == "service" || mode == "loadbalancer" || mode == "" {
		return &kubernetes.LBService{
			BaseService: baseService,
		}, nil
	}

	if mode == "ingress" {
		return &kubernetes.IngressService{
			BaseService: baseService,
		}, nil
	}

	if mode == "nginx-ingress" {
		return &kubernetes.IngressService{
			BaseService:       baseService,
			IngressClass:      "nginx",
			AnnotationsPrefix: "nginx.ingress.kubernetes.io",
			// TODO(wpjunior): may router opts plugged in here ?
		}, nil
	}

	if mode == "istio-gateway" {
		return &kubernetes.IstioGateway{
			BaseService: baseService,
		}, nil
	}

	return nil, errors.New("Mode not found")
}

func (m *MultiCluster) Healthcheck(ctx context.Context) error {
	return m.Fallback.Healthcheck(ctx)
}

func (m *MultiCluster) getKubeConfig(name, address string, timeout time.Duration) (*rest.Config, error) {
	selectedCluster := ClusterConfig{}

	for _, cluster := range m.Clusters {
		if cluster.Default {
			selectedCluster = cluster
		}
		if cluster.Name == name {
			selectedCluster = cluster
			break
		}
	}

	if selectedCluster.Name == "" {
		return nil, errors.New("cluster not found")
	}

	if selectedCluster.Address != "" {
		address = selectedCluster.Address
	}

	restConfig := &rest.Config{
		Host:        address,
		BearerToken: selectedCluster.Token,
		Timeout:     timeout,
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			return transport.DebugWrappers(observability.WrapTransport(rt))
		},
	}

	if selectedCluster.AuthProvider != "" {
		restConfig.AuthProvider = &api.AuthProviderConfig{
			Name: selectedCluster.AuthProvider,
		}
	}

	return restConfig, nil

}

func (m *MultiCluster) getToken(clusterName string) string {
	defaultToken := ""
	for _, cluster := range m.Clusters {
		if cluster.Default {
			defaultToken = cluster.Token
		}
		if cluster.Name == clusterName {
			return cluster.Token
		}
	}
	return defaultToken
}
