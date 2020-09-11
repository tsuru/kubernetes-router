// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backend

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/kubernetes"
	"github.com/tsuru/kubernetes-router/router"
)

var _ Backend = &fakeBackend{}

type fakeBackend struct{}

func (*fakeBackend) Router(mode string, headers http.Header) (router.Router, error) {
	return nil, errors.New("not implemented yet")
}
func (*fakeBackend) Healthcheck(context.Context) error {
	return errors.New("not implemented yet")
}

func TestMultiClusterFallback(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
	}
	router, err := backend.Router("service", http.Header{})
	if assert.Error(t, err) {
		assert.Equal(t, err.Error(), "not implemented yet")
	}
	assert.Nil(t, router)
}

func TestMultiClusterService(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:  "my-cluster",
				Token: "my-token",
			},
		},
	}
	router, err := backend.Router("service", http.Header{
		"X-Tsuru-Cluster-Name": {
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": {
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	lbService, ok := router.(*kubernetes.LBService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", lbService.BaseService.Namespace)
	assert.Equal(t, 10*time.Second, lbService.BaseService.Timeout)
	assert.Equal(t, "https://mycluster.com", lbService.BaseService.RestConfig.Host)
	assert.Equal(t, "my-token", lbService.BaseService.RestConfig.BearerToken)
}

func TestMultiClusterIngress(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:    "default-token",
				Token:   "my-token",
				Default: true,
			},
		},
	}
	router, err := backend.Router("ingress", http.Header{
		"X-Tsuru-Cluster-Name": {
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": {
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	ingressService, ok := router.(*kubernetes.IngressService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", ingressService.BaseService.Namespace)
	assert.Equal(t, "", ingressService.IngressClass)
	assert.Equal(t, 10*time.Second, ingressService.BaseService.Timeout)
	assert.Equal(t, "https://mycluster.com", ingressService.BaseService.RestConfig.Host)
	assert.Equal(t, "my-token", ingressService.BaseService.RestConfig.BearerToken)
}

func TestMultiClusterNginxIngress(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:    "default-token",
				Token:   "my-token",
				Default: true,
			},
		},
	}
	router, err := backend.Router("nginx-ingress", http.Header{
		"X-Tsuru-Cluster-Name": {
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": {
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	ingressService, ok := router.(*kubernetes.IngressService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", ingressService.BaseService.Namespace)
	assert.Equal(t, "nginx", ingressService.IngressClass)
	assert.Equal(t, "nginx.ingress.kubernetes.io", ingressService.AnnotationsPrefix)
	assert.Equal(t, 10*time.Second, ingressService.BaseService.Timeout)
	assert.Equal(t, "https://mycluster.com", ingressService.BaseService.RestConfig.Host)
	assert.Equal(t, "my-token", ingressService.BaseService.RestConfig.BearerToken)
}

func TestMultiClusterIstioGateway(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:    "default-token",
				Token:   "my-token",
				Default: true,
			},
		},
	}
	router, err := backend.Router("istio-gateway", http.Header{
		"X-Tsuru-Cluster-Name": {
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": {
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	istioGateway, ok := router.(*kubernetes.IstioGateway)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", istioGateway.BaseService.Namespace)
	assert.Equal(t, 10*time.Second, istioGateway.BaseService.Timeout)
	assert.Equal(t, "https://mycluster.com", istioGateway.BaseService.RestConfig.Host)
	assert.Equal(t, "my-token", istioGateway.BaseService.RestConfig.BearerToken)
}
