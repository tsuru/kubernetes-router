// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package backend

import (
	"context"
	"encoding/base64"
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
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
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
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
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

type dummyAuthProvider struct{}

func (d *dummyAuthProvider) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	return rt
}

func (d *dummyAuthProvider) Login() error {
	return nil
}

func TestMultiClusterAuthProvider(t *testing.T) {
	newDummyProvider := func(clusterAddress string, cfg map[string]string, persister restclient.AuthProviderConfigPersister) (restclient.AuthProvider, error) {
		return &dummyAuthProvider{}, nil
	}

	err := restclient.RegisterAuthProviderPlugin("dummy-test", newDummyProvider)
	require.NoError(t, err)

	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:         "my-cluster",
				Address:      "https://example.org",
				AuthProvider: &api.AuthProviderConfig{Name: "dummy-test"},
			},
		},
	}
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	router, err := backend.Router(spanCtx, "service", http.Header{
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	lbService, ok := router.(*kubernetes.LBService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", lbService.BaseService.Namespace)
	assert.Equal(t, 10*time.Second, lbService.BaseService.Timeout)
	assert.Equal(t, "https://example.org", lbService.BaseService.RestConfig.Host)
	assert.Equal(t, "dummy-test", lbService.BaseService.RestConfig.AuthProvider.Name)
	assert.Equal(t, span.(*mocktracer.MockSpan).Tags(), map[string]interface{}{
		"cluster.address": "https://mycluster.com",
		"cluster.name":    "my-cluster",
	})
}

func TestMultiClusterExecProvider(t *testing.T) {
	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:    "my-cluster",
				Address: "https://example.org",
				Exec: &api.ExecConfig{
					APIVersion: "client.authentication.k8s.io/v1beta1",
					Command:    "echo",
					Args:       []string{"arg1", "arg2"},
				},
			},
		},
	}
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	router, err := backend.Router(spanCtx, "service", http.Header{
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	lbService, ok := router.(*kubernetes.LBService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", lbService.BaseService.Namespace)
	assert.Equal(t, 10*time.Second, lbService.BaseService.Timeout)
	assert.Equal(t, "https://example.org", lbService.BaseService.RestConfig.Host)
	assert.Equal(t, "echo", lbService.BaseService.RestConfig.ExecProvider.Command)
	assert.Equal(t, []string{"arg1", "arg2"}, lbService.BaseService.RestConfig.ExecProvider.Args)
	assert.Equal(t, api.ExecInteractiveMode("Never"), lbService.BaseService.RestConfig.ExecProvider.InteractiveMode)
	assert.Equal(t, span.(*mocktracer.MockSpan).Tags(), map[string]interface{}{
		"cluster.address": "https://mycluster.com",
		"cluster.name":    "my-cluster",
	})
}

func TestMultiClusterSetBothAuthMechanism(t *testing.T) {
	newDummyProvider := func(clusterAddress string, cfg map[string]string, persister restclient.AuthProviderConfigPersister) (restclient.AuthProvider, error) {
		return &dummyAuthProvider{}, nil
	}

	err := restclient.RegisterAuthProviderPlugin("dummy-test2", newDummyProvider)
	require.NoError(t, err)

	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:         "my-cluster",
				Address:      "https://example.org",
				AuthProvider: &api.AuthProviderConfig{Name: "dummy-test2"},
				Exec: &api.ExecConfig{
					Command: "echo",
				},
			},
		},
	}
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	_, err = backend.Router(spanCtx, "service", http.Header{
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
			"https://mycluster.com",
		},
	})
	assert.Error(t, err, "both exec and authProvider mutually exclusive are set in the cluster config")
}

func TestMultiClusterCA(t *testing.T) {
	fakeCA := `-----BEGIN CERTIFICATE-----
MIIGFDCCA/ygAwIBAgIIU+w77vuySF8wDQYJKoZIhvcNAQEFBQAwUTELMAkGA1UE
BhMCRVMxQjBABgNVBAMMOUF1dG9yaWRhZCBkZSBDZXJ0aWZpY2FjaW9uIEZpcm1h
cHJvZmVzaW9uYWwgQ0lGIEE2MjYzNDA2ODAeFw0wOTA1MjAwODM4MTVaFw0zMDEy
MzEwODM4MTVaMFExCzAJBgNVBAYTAkVTMUIwQAYDVQQDDDlBdXRvcmlkYWQgZGUg
Q2VydGlmaWNhY2lvbiBGaXJtYXByb2Zlc2lvbmFsIENJRiBBNjI2MzQwNjgwggIi
MA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQDKlmuO6vj78aI14H9M2uDDUtd9
thDIAl6zQyrET2qyyhxdKJp4ERppWVevtSBC5IsP5t9bpgOSL/UR5GLXMnE42QQM
cas9UX4PB99jBVzpv5RvwSmCwLTaUbDBPLutN0pcyvFLNg4kq7/DhHf9qFD0sefG
L9ItWY16Ck6WaVICqjaY7Pz6FIMMNx/Jkjd/14Et5cS54D40/mf0PmbR0/RAz15i
NA9wBj4gGFrO93IbJWyTdBSTo3OxDqqHECNZXyAFGUftaI6SEspd/NYrspI8IM/h
X68gvqB2f3bl7BqGYTM+53u0P6APjqK5am+5hyZvQWyIplD9amML9ZMWGxmPsu2b
m8mQ9QEM3xk9Dz44I8kvjwzRAv4bVdZO0I08r0+k8/6vKtMFnXkIoctXMbScyJCy
Z/QYFpM6/EfY0XiWMR+6KwxfXZmtY4laJCB22N/9q06mIqqdXuYnin1oKaPnirja
EbsXLZmdEyRG98Xi2J+Of8ePdG1asuhy9azuJBCtLxTa/y2aRnFHvkLfuwHb9H/T
KI8xWVvTyQKmtFLKbpf7Q8UIJm+K9Lv9nyiqDdVF8xM6HdjAeI9BZzwelGSuewvF
6NkBiDkal4ZkQdU7hwxu+g/GvUgUvzlN1J5Bto+WHWOWk9mVBngxaJ43BjuAiUVh
OSPHG0SjFeUc+JIwuwIDAQABo4HvMIHsMBIGA1UdEwEB/wQIMAYBAf8CAQEwDgYD
VR0PAQH/BAQDAgEGMB0GA1UdDgQWBBRlzeurNR4APn7VdMActHNHDhpkLzCBpgYD
VR0gBIGeMIGbMIGYBgRVHSAAMIGPMC8GCCsGAQUFBwIBFiNodHRwOi8vd3d3LmZp
cm1hcHJvZmVzaW9uYWwuY29tL2NwczBcBggrBgEFBQcCAjBQHk4AUABhAHMAZQBv
ACAAZABlACAAbABhACAAQgBvAG4AYQBuAG8AdgBhACAANAA3ACAAQgBhAHIAYwBl
AGwAbwBuAGEAIAAwADgAMAAxADcwDQYJKoZIhvcNAQEFBQADggIBABd9oPm03cXF
661LJLWhAqvdpYhKsg9VSytXjDvlMd3+xDLx51tkljYyGOylMnfX40S2wBEqgLk9
am58m9Ot/MPWo+ZkKXzR4Tgegiv/J2Wv+xYVxC5xhOW1//qkR71kMrv2JYSiJ0L1
ILDCExARzRAVukKQKtJE4ZYm6zFIEv0q2skGz3QeqUvVhyj5eTSSPi5E6PaPT481
PyWzOdxjKpBrIF/EUhJOlywqrJ2X3kjyo2bbwtKDlaZmp54lD+kLM5FlClrD2VQS
3a/DTg4fJl4N3LON7NWBcN7STyQF82xO9UxJZo3R/9ILJUFI/lGExkKvgATP0H5k
SeTy36LssUzAKh3ntLFlosS88Zj0qnAHY7S42jtM+kAiMFsRpvAFDsYCA0irhpuF
3dvd6qJ2gHN99ZwExEWN57kci57q13XRcrHedUTnQn3iV2t93Jm8PYMo6oCTjcVM
ZcFwgbg4/EMxsvYDNEeyrPsiBsse3RdHHF9mudMaotoRsaS8I8nkvof/uZS2+F0g
StRf571oe2XyFR7SOqkt6dhrJKyXWERHrVkY8SFlcN7ONGCoQPHzPKTDKCOM/icz
Q0CgFzzr6juwcqajuUpLXhZI9LK8yIySxZ2frHI2vDSANGupi5LAuBft7HZT9SQB
jLMi6Et8Vcad+qMUu2WFbm5PEn4KPJ2V
-----END CERTIFICATE-----`

	encodedFakeCA := base64.StdEncoding.EncodeToString([]byte(fakeCA))

	backend := &MultiCluster{
		Namespace: "tsuru-test",
		Fallback:  &fakeBackend{},
		Clusters: []ClusterConfig{
			{
				Name:    "my-cluster",
				Address: "https://example.org",
				CA:      encodedFakeCA,
			},
		},
	}
	mockTracer := mocktracer.New()
	span := mockTracer.StartSpan("test")
	spanCtx := opentracing.ContextWithSpan(ctx, span)
	router, err := backend.Router(spanCtx, "service", http.Header{
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
			"https://mycluster.com",
		},
	})
	assert.NoError(t, err)
	lbService, ok := router.(*kubernetes.LBService)
	require.True(t, ok)
	assert.Equal(t, "tsuru-test", lbService.BaseService.Namespace)
	assert.Equal(t, 10*time.Second, lbService.BaseService.Timeout)
	assert.Equal(t, "https://example.org", lbService.BaseService.RestConfig.Host)
	assert.Equal(t, []byte(fakeCA), lbService.BaseService.RestConfig.TLSClientConfig.CAData)
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
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
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
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
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
		"X-Tsuru-Cluster-Name": []string{
			"my-cluster",
		},
		"X-Tsuru-Cluster-Addresses": []string{
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
