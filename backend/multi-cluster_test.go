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

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/kubernetes"
	"github.com/tsuru/kubernetes-router/router"
)

var _ Backend = &fakeBackend{}
var ctx = context.TODO()

type fakeBackend struct{}

func (*fakeBackend) Router(ctx context.Context, mode string, headers http.Header) (router.Router, error) {
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
	router, err := backend.Router(ctx, "service", http.Header{})
	if assert.Error(t, err) {
		assert.Equal(t, err.Error(), "not implemented yet")
	}
	assert.Nil(t, router)
}

func TestMultiClusterHealthcheck(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
	}
	err := backend.Healthcheck(context.TODO())
	if assert.Error(t, err) {
		assert.Equal(t, err.Error(), "not implemented yet")
	}
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
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	router, err := backend.Router(spanCtx, "service", http.Header{
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
	assert.Equal(t, span.(*mocktracer.MockSpan).Tags(), map[string]interface{}{
		"cluster.address": "https://mycluster.com",
		"cluster.name":    "my-cluster",
	})
}

func TestMultiClusterAuthProvider(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:         "my-cluster",
				Address:      "https://example.org",
				AuthProvider: "gcp",
			},
		},
	}
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	router, err := backend.Router(spanCtx, "service", http.Header{
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
	assert.Equal(t, "https://example.org", lbService.BaseService.RestConfig.Host)
	assert.Equal(t, "gcp", lbService.BaseService.RestConfig.AuthProvider.Name)
	assert.Equal(t, span.(*mocktracer.MockSpan).Tags(), map[string]interface{}{
		"cluster.address": "https://mycluster.com",
		"cluster.name":    "my-cluster",
	})
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
	router, err := backend.Router(ctx, "ingress", http.Header{
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
	router, err := backend.Router(ctx, "nginx-ingress", http.Header{
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
	router, err := backend.Router(ctx, "istio-gateway", http.Header{
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
