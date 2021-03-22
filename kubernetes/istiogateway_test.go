// Copyright 2018 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	apiNetworking "istio.io/api/networking/v1beta1"
	networking "istio.io/client-go/pkg/apis/networking/v1beta1"
	fakeistio "istio.io/client-go/pkg/clientset/versioned/fake"
	networkingClientSet "istio.io/client-go/pkg/clientset/versioned/typed/networking/v1beta1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeService() (IstioGateway, networkingClientSet.NetworkingV1beta1Interface) {
	fakeIstio := fakeistio.NewSimpleClientset().NetworkingV1beta1()
	return IstioGateway{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           fake.NewSimpleClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
		istioClient:     fakeIstio,
		DomainSuffix:    "my.domain",
		GatewaySelector: map[string]string{"istio": "ingress"},
	}, fakeIstio
}

func TestIstioGateway_Ensure(t *testing.T) {
	svc, istio := fakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "myapp-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
	gateway, err := istio.Gateways("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, gateway.Labels)
	assert.Equal(t, map[string]string{}, gateway.Annotations)
	assert.Equal(t, apiNetworking.Gateway{
		Servers: []*apiNetworking.Server{
			{
				Port: &apiNetworking.Port{
					Number:   80,
					Name:     "http2",
					Protocol: "HTTP2",
				},
				Hosts: []string{
					"*",
				},
			},
		},
		Selector: map[string]string{
			"istio": "ingress",
		},
	}, gateway.Spec)
	assert.Equal(t, map[string]string{
		"tsuru.io/app-name":                      "myapp",
		"router.tsuru.io/base-service-name":      "myapp-web",
		"router.tsuru.io/base-service-namespace": "default",
	}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{}, virtualSvc.Annotations)
	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"mesh",
			"myapp",
		},
		Hosts: []string{
			"myapp.my.domain",
			"myapp-web",
		},
		Http: []*apiNetworking.HTTPRoute{
			{
				Route: []*apiNetworking.HTTPRouteDestination{
					{
						Destination: &apiNetworking.Destination{
							Host: "myapp-web",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_EnsureWithCNames(t *testing.T) {
	svc, istio := fakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		CNames: []string{"test.io", "www.test.io"},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "myapp-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
	gateway, err := istio.Gateways("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, gateway.Labels)
	assert.Equal(t, map[string]string{}, gateway.Annotations)
	assert.Equal(t, apiNetworking.Gateway{
		Servers: []*apiNetworking.Server{
			{
				Port: &apiNetworking.Port{
					Number:   80,
					Name:     "http2",
					Protocol: "HTTP2",
				},
				Hosts: []string{
					"*",
				},
			},
		},
		Selector: map[string]string{
			"istio": "ingress",
		},
	}, gateway.Spec)
	assert.Equal(t, map[string]string{
		"tsuru.io/app-name":                      "myapp",
		"router.tsuru.io/base-service-name":      "myapp-web",
		"router.tsuru.io/base-service-namespace": "default",
	}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{
		"tsuru.io/additional-hosts": "test.io,www.test.io",
	}, virtualSvc.Annotations)
	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"mesh",
			"myapp",
		},
		Hosts: []string{
			"myapp.my.domain",
			"myapp-web",
			"test.io",
			"www.test.io",
		},
		Http: []*apiNetworking.HTTPRoute{
			{
				Route: []*apiNetworking.HTTPRouteDestination{
					{
						Destination: &apiNetworking.Destination{
							Host: "myapp-web",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_Create_existingVirtualService(t *testing.T) {
	svc, istio := fakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	_, err = istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp",
		},
		Spec: apiNetworking.VirtualService{
			Hosts: []string{"older-host"},
			Http: []*apiNetworking.HTTPRoute{
				{
					Route: []*apiNetworking.HTTPRouteDestination{
						{
							Destination: &apiNetworking.Destination{
								Host: "to-be-keep",
							},
							Weight: 100,
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "myapp-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)

	gateway, err := istio.Gateways("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, gateway.Labels)
	assert.Equal(t, map[string]string{}, gateway.Annotations)

	assert.Equal(t, apiNetworking.Gateway{
		Servers: []*apiNetworking.Server{
			{
				Port: &apiNetworking.Port{
					Number:   80,
					Name:     "http2",
					Protocol: "HTTP2",
				},
				Hosts: []string{
					"*",
				},
			},
		},
		Selector: map[string]string{
			"istio": "ingress",
		},
	}, gateway.Spec)
	assert.Equal(t, map[string]string{
		"tsuru.io/app-name":                      "myapp",
		"router.tsuru.io/base-service-name":      "myapp-web",
		"router.tsuru.io/base-service-namespace": "default",
	}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{}, virtualSvc.Annotations)
	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"myapp",
		},
		Hosts: []string{
			"older-host",
			"myapp.my.domain",
			"myapp-web",
		},
		Http: []*apiNetworking.HTTPRoute{
			{
				Route: []*apiNetworking.HTTPRouteDestination{
					{
						Destination: &apiNetworking.Destination{
							Host: "to-be-keep",
						},
						Weight: 100,
					},
					{
						Destination: &apiNetworking.Destination{
							Host: "myapp-web",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_CNameLifeCycle(t *testing.T) {
	tests := []struct {
		annotation         string
		hosts              []string
		ensureCNames       []string
		expectedHosts      []string
		expectedAnnotation string
	}{
		{
			hosts:        []string{"existing1"},
			ensureCNames: []string{"myhost.com"},
			expectedHosts: []string{
				"existing1",
				"myapp.my.domain",
				"myapp-web",
				"myhost.com",
			},
			expectedAnnotation: "myhost.com",
		},
		{
			annotation:   "my.other.addr",
			hosts:        []string{"existing1"},
			ensureCNames: []string{"myhost.com"},
			expectedHosts: []string{
				"existing1",
				"myapp.my.domain",
				"myapp-web",
				"myhost.com",
			},
			expectedAnnotation: "myhost.com",
		},
		{
			annotation:   "my.other.addr",
			hosts:        []string{"existing1", "my.other.addr"},
			ensureCNames: []string{"my.other.addr", "myhost.com"},
			expectedHosts: []string{
				"myhost.com",
				"existing1",
				"myapp.my.domain",
				"myapp-web",
				"my.other.addr",
			},
			expectedAnnotation: "my.other.addr,myhost.com",
		},
		{
			annotation:   "my.other.addr,myhost.com",
			hosts:        []string{"existing1", "my.other.addr"},
			ensureCNames: []string{"another.host.com", "my.other.addr", "myhost.com"},
			expectedHosts: []string{
				"myhost.com", "existing1", "my.other.addr", "another.host.com",
				"myapp.my.domain",
				"myapp-web",
			},
			expectedAnnotation: "another.host.com,my.other.addr,myhost.com",
		},
		{
			annotation:   "my.other.addr,myhost.com",
			hosts:        []string{"existing1", "my.other.addr"},
			ensureCNames: []string{},
			expectedHosts: []string{
				"existing1",
				"myapp.my.domain",
				"myapp-web",
			},
			expectedAnnotation: "",
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
			svc, istio := fakeService()
			err := createAppWebService(svc.Client, svc.Namespace, "myapp")
			require.NoError(t, err)
			_, err = istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
				ObjectMeta: metav1.ObjectMeta{
					Name: "myapp",
					Labels: map[string]string{
						"tsuru.io/app-name": "myapp",
					},
					Annotations: map[string]string{
						"tsuru.io/additional-hosts": tt.annotation,
					},
				},
				Spec: apiNetworking.VirtualService{
					Hosts: tt.hosts,
					Http: []*apiNetworking.HTTPRoute{
						{
							Route: []*apiNetworking.HTTPRouteDestination{
								{
									Destination: &apiNetworking.Destination{
										Host: "to-be-keep",
									},
								},
							},
						},
					},
				},
			}, metav1.CreateOptions{})

			require.NoError(t, err)
			err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
				CNames: tt.ensureCNames,
				Prefixes: []router.BackendPrefix{
					{
						Target: router.BackendTarget{
							Service:   "myapp-web",
							Namespace: svc.Namespace,
						},
					},
				},
			})
			require.NoError(t, err)
			virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedHosts, virtualSvc.Spec.Hosts)
			assert.Equal(t, tt.expectedAnnotation, virtualSvc.Annotations["tsuru.io/additional-hosts"])
		})
	}
}
