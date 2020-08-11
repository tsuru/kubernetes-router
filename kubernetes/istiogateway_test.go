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
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/networking/core/v1alpha3/fakes"
	apiv1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeService() (IstioGateway, *fakes.IstioConfigStore) {
	fakeIstio := &fakes.IstioConfigStore{}
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
	err := svc.Create(idForApp("myapp"), router.Opts{})
	require.NoError(t, err)
	require.Equal(t, 2, istio.CreateCallCount())
	gatewayConfig := istio.CreateArgsForCall(0)
	vsConfig := istio.CreateArgsForCall(1)
	assert.Equal(t, model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:      "gateway",
			Group:     "networking.istio.io",
			Version:   "v1alpha3",
			Name:      "myapp",
			Namespace: "default",
			Labels: map[string]string{
				"tsuru.io/app-name": "myapp",
			},
			Annotations: map[string]string{},
		},
		Spec: &networking.Gateway{
			Servers: []*networking.Server{
				{
					Port: &networking.Port{
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
		},
	}, gatewayConfig)
	assert.Equal(t, model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:      "virtual-service",
			Group:     "networking.istio.io",
			Version:   "v1alpha3",
			Name:      "myapp",
			Namespace: "default",
			Labels: map[string]string{
				"tsuru.io/app-name": "myapp",
			},
			Annotations: map[string]string{},
		},
		Spec: &networking.VirtualService{
			Gateways: []string{
				"mesh",
				"myapp",
			},
			Hosts: []string{
				"myapp.my.domain",
			},
			Http: []*networking.HTTPRoute{
				{
					Route: []*networking.DestinationWeight{
						{
							Destination: &networking.Destination{
								Host: "kubernetes-router-placeholder",
							},
						},
					},
				},
			},
		},
	}, vsConfig)
}

func TestIstioGateway_Create_existingVirtualService(t *testing.T) {
	svc, istio := fakeService()
	istio.GetReturns(&model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:        "virtual-service",
			Group:       "networking.istio.io",
			Version:     "v1alpha3",
			Name:        "myapp",
			Namespace:   "default",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: &networking.VirtualService{},
	}, true)
	err := svc.Create(idForApp("myapp"), router.Opts{})
	require.NoError(t, err)
	require.Equal(t, 1, istio.CreateCallCount())
	require.Equal(t, 1, istio.UpdateCallCount())
	gatewayConfig := istio.CreateArgsForCall(0)
	vsConfig := istio.UpdateArgsForCall(0)
	assert.Equal(t, model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:      "gateway",
			Group:     "networking.istio.io",
			Version:   "v1alpha3",
			Name:      "myapp",
			Namespace: "default",
			Labels: map[string]string{
				"tsuru.io/app-name": "myapp",
			},
			Annotations: map[string]string{},
		},
		Spec: &networking.Gateway{
			Servers: []*networking.Server{
				{
					Port: &networking.Port{
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
		},
	}, gatewayConfig)
	assert.Equal(t, model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:      "virtual-service",
			Group:     "networking.istio.io",
			Version:   "v1alpha3",
			Name:      "myapp",
			Namespace: "default",
			Labels: map[string]string{
				"tsuru.io/app-name": "myapp",
			},
			Annotations: map[string]string{},
		},
		Spec: &networking.VirtualService{
			Gateways: []string{
				"myapp",
			},
			Hosts: []string{
				"myapp.my.domain",
			},
			Http: []*networking.HTTPRoute{
				{
					Route: []*networking.DestinationWeight{
						{
							Destination: &networking.Destination{
								Host: "kubernetes-router-placeholder",
							},
						},
					},
				},
			},
		},
	}, vsConfig)
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
	_, err := svc.Client.CoreV1().Services(svc.Namespace).Create(&webSvc)
	require.NoError(t, err)
	istio.GetReturns(&model.Config{
		ConfigMeta: model.ConfigMeta{
			Type:        "virtual-service",
			Group:       "networking.istio.io",
			Version:     "v1alpha3",
			Name:        "myapp",
			Namespace:   "default",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
		Spec: &networking.VirtualService{
			Gateways: []string{
				"myapp",
			},
			Hosts: []string{
				"myapp.my.domain",
			},
			Http: []*networking.HTTPRoute{
				{
					Route: []*networking.DestinationWeight{
						{
							Destination: &networking.Destination{
								Host: "kubernetes-router-placeholder",
							},
						},
					},
				},
			},
		},
	}, true)
	err = svc.Update(idForApp("myapp"), router.RoutesRequestExtraData{})
	require.NoError(t, err)
	require.Equal(t, 1, istio.UpdateCallCount())
	vsConfig := istio.UpdateArgsForCall(0)
	assert.Equal(t, &networking.VirtualService{
		Gateways: []string{
			"myapp",
		},
		Hosts: []string{
			"myapp.my.domain",
			"myapp-single",
		},
		Http: []*networking.HTTPRoute{
			{
				Route: []*networking.DestinationWeight{
					{
						Destination: &networking.Destination{
							Host: "myapp-single",
						},
					},
				},
			},
		},
	}, vsConfig.Spec)
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
			istio.GetReturns(&model.Config{
				ConfigMeta: model.ConfigMeta{
					Type:      "virtual-service",
					Group:     "networking.istio.io",
					Version:   "v1alpha3",
					Name:      "myapp",
					Namespace: "default",
					Labels: map[string]string{
						"tsuru.io/app-name": "myapp",
					},
					Annotations: map[string]string{
						"tsuru.io/additional-hosts": tt.annotation,
					},
				},
				Spec: &networking.VirtualService{
					Hosts: tt.hosts,
				},
			}, true)
			err := svc.SetCname(idForApp("myapp"), tt.toAdd)
			require.NoError(t, err)
			require.Equal(t, 1, istio.UpdateCallCount())
			vsConfig := istio.UpdateArgsForCall(0)
			assert.ElementsMatch(t, tt.expectedHosts, vsConfig.Spec.(*networking.VirtualService).Hosts)
			assert.Equal(t, tt.expectedAnnotation, vsConfig.Annotations["tsuru.io/additional-hosts"])
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
			istio.GetReturns(&model.Config{
				ConfigMeta: model.ConfigMeta{
					Type:      "virtual-service",
					Group:     "networking.istio.io",
					Version:   "v1alpha3",
					Name:      "myapp",
					Namespace: "default",
					Labels: map[string]string{
						"tsuru.io/app-name": "myapp",
					},
					Annotations: map[string]string{
						"tsuru.io/additional-hosts": tt.annotation,
					},
				},
				Spec: &networking.VirtualService{
					Hosts: tt.hosts,
				},
			}, true)
			err := svc.UnsetCname(idForApp("myapp"), tt.toRemove)
			require.NoError(t, err)
			require.Equal(t, 1, istio.UpdateCallCount())
			vsConfig := istio.UpdateArgsForCall(0)
			assert.ElementsMatch(t, tt.expectedHosts, vsConfig.Spec.(*networking.VirtualService).Hosts)
			assert.Equal(t, tt.expectedAnnotation, vsConfig.Annotations["tsuru.io/additional-hosts"])
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
			istio.GetReturns(&model.Config{
				ConfigMeta: model.ConfigMeta{
					Type:      "virtual-service",
					Group:     "networking.istio.io",
					Version:   "v1alpha3",
					Name:      "myapp",
					Namespace: "default",
					Labels: map[string]string{
						"tsuru.io/app-name": "myapp",
					},
					Annotations: map[string]string{
						"tsuru.io/additional-hosts": tt.annotation,
					},
				},
				Spec: &networking.VirtualService{},
			}, true)
			rsp, err := svc.GetCnames(idForApp("myapp"))
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.expected, rsp.Cnames)
		})
	}
}
