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
	apiv1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
		DefaultDomain:   "my.domain",
		GatewaySelector: map[string]string{"istio": "ingress"},
	}, fakeIstio
}

func TestIstioGateway_Create(t *testing.T) {
	svc, istio := fakeService()
	err := svc.Create(ctx, idForApp("myapp"), router.Opts{})
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
	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{}, virtualSvc.Annotations)
	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"mesh",
			"myapp",
		},
		Hosts: []string{
			"myapp.my.domain",
		},
		Http: []*apiNetworking.HTTPRoute{
			{
				Route: []*apiNetworking.HTTPRouteDestination{
					{
						Destination: &apiNetworking.Destination{
							Host: "kubernetes-router-placeholder",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_Create_existingVirtualService(t *testing.T) {
	svc, istio := fakeService()

	_, err := istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
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

	err = svc.Create(ctx, idForApp("myapp"), router.Opts{})
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
	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{}, virtualSvc.Annotations)
	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"myapp",
		},
		Hosts: []string{
			"older-host",
			"myapp.my.domain",
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
							Host: "kubernetes-router-placeholder",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_Update(t *testing.T) {
	svc, istio := fakeService()
	webSvc := apiv1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      "myapp-single",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "myapp"},
	},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899)}},
		},
	}
	_, err := svc.Client.CoreV1().Services(svc.Namespace).Create(ctx, &webSvc, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name: "myapp",
		},
		Spec: apiNetworking.VirtualService{
			Gateways: []string{
				"myapp",
			},
			Hosts: []string{
				"myapp.my.domain",
			},
			Http: []*apiNetworking.HTTPRoute{
				{
					Route: []*apiNetworking.HTTPRouteDestination{
						{
							Destination: &apiNetworking.Destination{
								Host: "kubernetes-router-placeholder",
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = svc.Update(ctx, idForApp("myapp"), router.RoutesRequestExtraData{})
	require.NoError(t, err)

	virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"tsuru.io/app-name": "myapp"}, virtualSvc.Labels)
	assert.Equal(t, map[string]string{}, virtualSvc.Annotations)

	assert.Equal(t, apiNetworking.VirtualService{
		Gateways: []string{
			"myapp",
		},
		Hosts: []string{
			"myapp.my.domain",
			"myapp-single",
		},
		Http: []*apiNetworking.HTTPRoute{
			{
				Route: []*apiNetworking.HTTPRouteDestination{
					{
						Destination: &apiNetworking.Destination{
							Host: "myapp-single",
						},
					},
				},
			},
		},
	}, virtualSvc.Spec)
}

func TestIstioGateway_SetCname(t *testing.T) {
	tests := []struct {
		annotation         string
		hosts              []string
		toAdd              string
		expectedHosts      []string
		expectedAnnotation string
	}{
		{
			hosts:              []string{"existing1"},
			toAdd:              "myhost.com",
			expectedHosts:      []string{"myhost.com", "existing1"},
			expectedAnnotation: "myhost.com",
		},
		{
			annotation:         "my.other.addr",
			hosts:              []string{"existing1"},
			toAdd:              "myhost.com",
			expectedHosts:      []string{"myhost.com", "existing1", "my.other.addr"},
			expectedAnnotation: "my.other.addr,myhost.com",
		},
		{
			annotation:         "my.other.addr",
			hosts:              []string{"existing1", "my.other.addr"},
			toAdd:              "myhost.com",
			expectedHosts:      []string{"myhost.com", "existing1", "my.other.addr"},
			expectedAnnotation: "my.other.addr,myhost.com",
		},
		{
			annotation:         "my.other.addr,myhost.com",
			hosts:              []string{"existing1", "my.other.addr"},
			toAdd:              "another.host.com",
			expectedHosts:      []string{"myhost.com", "existing1", "my.other.addr", "another.host.com"},
			expectedAnnotation: "another.host.com,my.other.addr,myhost.com",
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
			svc, istio := fakeService()
			_, err := istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
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
			err = svc.SetCname(ctx, idForApp("myapp"), tt.toAdd)
			require.NoError(t, err)
			virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedHosts, virtualSvc.Spec.Hosts)
			assert.Equal(t, tt.expectedAnnotation, virtualSvc.Annotations["tsuru.io/additional-hosts"])
		})
	}
}

func TestIstioGateway_UnsetCname(t *testing.T) {
	tests := []struct {
		annotation         string
		hosts              []string
		toRemove           string
		expectedHosts      []string
		expectedAnnotation string
	}{
		{
			hosts:              []string{"existing1"},
			toRemove:           "myhost.com",
			expectedHosts:      []string{"existing1"},
			expectedAnnotation: "",
		},
		{
			annotation:         "my.other.addr,myhost.com",
			hosts:              []string{"myhost.com", "existing1"},
			toRemove:           "myhost.com",
			expectedHosts:      []string{"existing1", "my.other.addr"},
			expectedAnnotation: "my.other.addr",
		},
		{
			annotation:         "my.other.addr,myhost.com",
			hosts:              []string{"myhost.com", "existing1", "my.other.addr"},
			toRemove:           "myhost.com",
			expectedHosts:      []string{"existing1", "my.other.addr"},
			expectedAnnotation: "my.other.addr",
		},
		{
			annotation:         "another.host.com,my.other.addr,myhost.com",
			hosts:              []string{"myhost.com", "existing1", "my.other.addr", "another.host.com"},
			toRemove:           "another.host.com",
			expectedHosts:      []string{"existing1", "my.other.addr", "myhost.com"},
			expectedAnnotation: "my.other.addr,myhost.com",
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
			svc, istio := fakeService()
			_, err := istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
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

			err = svc.UnsetCname(ctx, idForApp("myapp"), tt.toRemove)
			require.NoError(t, err)
			virtualSvc, err := istio.VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expectedHosts, virtualSvc.Spec.Hosts)
			assert.Equal(t, tt.expectedAnnotation, virtualSvc.Annotations["tsuru.io/additional-hosts"])
		})
	}
}

func TestIstioGateway_GetCnames(t *testing.T) {
	tests := []struct {
		annotation string
		expected   []string
	}{
		{},
		{
			annotation: "my.other.addr,myhost.com",
			expected:   []string{"my.other.addr", "myhost.com"},
		},
		{
			annotation: "my.other.addr,",
			expected:   []string{"my.other.addr"},
		},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprintf("test %d", i), func(t *testing.T) {
			svc, istio := fakeService()
			_, err := istio.VirtualServices("default").Create(ctx, &networking.VirtualService{
				ObjectMeta: metav1.ObjectMeta{
					Name: "myapp",
					Labels: map[string]string{
						"tsuru.io/app-name": "myapp",
					},
					Annotations: map[string]string{
						"tsuru.io/additional-hosts": tt.annotation,
					},
				},
				Spec: apiNetworking.VirtualService{},
			}, metav1.CreateOptions{})
			require.NoError(t, err)
			rsp, err := svc.GetCnames(ctx, idForApp("myapp"))
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, rsp.Cnames)
		})
	}
}
