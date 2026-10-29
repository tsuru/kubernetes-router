// Copyright 2026 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	apiNetworking "istio.io/api/networking/v1beta1"
	istionetworking "istio.io/client-go/pkg/apis/networking/v1beta1"
	fakeistio "istio.io/client-go/pkg/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeVirtualServiceService() (*IstioVirtualService, *fakeistio.Clientset) {
	client := fakeistio.NewSimpleClientset()
	return &IstioVirtualService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           fake.NewSimpleClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
		istioClient:  client.NetworkingV1beta1(),
		DomainSuffix: "my.domain",
		GatewayName:  "shared-gateway",
	}, client
}

func TestIstioVirtualServiceEnsureReconcilesDefaultDestination(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	createTarget := func(name string) {
		_, err := svc.Client.CoreV1().Services("default").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	createTarget("service-a")
	createTarget("service-b")

	ensure := func(service string) {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: service, Namespace: "default"}}},
		}))
	}
	ensure("service-a")

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualService.Spec.Http[0].Route = append(virtualService.Spec.Http[0].Route, &apiNetworking.HTTPRouteDestination{
		Destination: &apiNetworking.Destination{Host: "manually-configured"},
		Weight:      20,
	})
	virtualService.Spec.Http[0].Route[0].Weight = 80
	_, err = client.NetworkingV1beta1().VirtualServices("default").Update(ctx, virtualService, metav1.UpdateOptions{})
	require.NoError(t, err)

	ensure("service-b")
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, virtualService.Spec.Http[0].Route, 2)
	assert.Equal(t, "service-b", virtualService.Spec.Http[0].Route[0].Destination.Host)
	assert.Equal(t, int32(80), virtualService.Spec.Http[0].Route[0].Weight)
	assert.Equal(t, "manually-configured", virtualService.Spec.Http[0].Route[1].Destination.Host)
	assert.Equal(t, int32(20), virtualService.Spec.Http[0].Route[1].Weight)
	assert.Equal(t, "service-b", virtualService.Labels[appBaseServiceNameLabel])
	assert.Equal(t, int32(100), virtualService.Spec.Http[0].Route[0].Weight+virtualService.Spec.Http[0].Route[1].Weight)
}

func TestIstioVirtualServiceEnsureUsesFQDNForCrossNamespaceBackend(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	_, err := svc.Client.CoreV1().Services("remote").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: "remote-web"}}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "remote-web", Namespace: "remote"}}},
	})
	require.NoError(t, err)
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "remote-web.remote.svc.cluster.local", virtualService.Spec.Http[0].Route[0].Destination.Host)
	assert.Contains(t, virtualService.Spec.Hosts, "remote-web.remote.svc.cluster.local")
}

func TestIstioVirtualServiceRequestGatewayOverrideKeepsConfiguredDomain(t *testing.T) {
	svc, _ := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts:     router.Opts{GatewayName: "request-gateway", GatewayNamespace: "request-ns"},
		Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
	}))
	addresses, err := svc.GetAddresses(ctx, idForApp("myapp"))
	require.NoError(t, err)
	assert.Equal(t, []string{"myapp.my.domain"}, addresses)
}

func TestIstioVirtualServiceEnsureReferencesExistingGateway(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))

	err := svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts:   router.Opts{GatewayName: "request-gateway", GatewayNamespace: "edge"},
		CNames: []string{"www.example.com"},
		Prefixes: []router.BackendPrefix{{
			Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"},
		}},
	})
	require.NoError(t, err)

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"edge/request-gateway", "mesh"}, virtualService.Spec.Gateways)
	assert.ElementsMatch(t, []string{"myapp-web", "myapp.my.domain", "www.example.com"}, virtualService.Spec.Hosts)
	assert.Equal(t, "www.example.com", virtualService.Annotations[hostsAnnotation])
	_, err = client.NetworkingV1beta1().Gateways("edge").Get(ctx, "request-gateway", metav1.GetOptions{})
	assert.Error(t, err, "Ensure must not create the referenced Gateway")
}

func TestIstioVirtualServiceEnsureKeepsExistingGatewayReferences(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Gateways: []string{"mesh", "other-ns/other-gateway", "shared-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{{
			Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"},
		}},
	})
	require.NoError(t, err)
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "other-ns/other-gateway", "shared-gateway"}, virtualService.Spec.Gateways)
}

func TestIstioVirtualServiceEnsureMissingGatewayNameDoesNotMutate(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	svc.GatewayName = ""
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))

	err := svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{{
			Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"},
		}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway name")
	_, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	assert.Error(t, err, "missing Gateway name must fail before creating a VirtualService")
	for _, action := range client.Actions() {
		assert.NotContains(t, []string{"create", "update", "patch", "delete"}, action.GetVerb(), "missing Gateway name must not mutate Istio resources")
	}
}

func TestIstioVirtualServiceRemoveDoesNotDeleteGateway(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	_, err := client.NetworkingV1beta1().Gateways("edge").Create(ctx, &istionetworking.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-gateway"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "myapp",
			Annotations: map[string]string{gatewayReferenceAnnotation: "edge/shared-gateway"},
		},
		Spec: apiNetworking.VirtualService{Gateways: []string{"mesh", "edge/shared-gateway", "other-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(t, svc.Remove(ctx, idForApp("myapp")))
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "other-gateway"}, virtualService.Spec.Gateways)
	_, err = client.NetworkingV1beta1().Gateways("edge").Get(ctx, "shared-gateway", metav1.GetOptions{})
	require.NoError(t, err, "Remove must leave the externally managed Gateway intact")
	for _, action := range client.Actions() {
		assert.False(t, action.GetVerb() == "delete" && action.GetResource().Resource == "gateways", "Remove must not delete a Gateway")
	}
}

func TestIstioVirtualServiceRemoveUsesRecordedGatewayWithoutStartupConfig(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	svc.GatewayName = ""
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "myapp",
			Annotations: map[string]string{gatewayReferenceAnnotation: "edge/request-gateway"},
		},
		Spec: apiNetworking.VirtualService{Gateways: []string{"mesh", "edge/request-gateway", "other-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(t, svc.Remove(ctx, idForApp("myapp")))
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "other-gateway"}, virtualService.Spec.Gateways)
	assert.NotContains(t, virtualService.Annotations, gatewayReferenceAnnotation)
}

func TestIstioVirtualServiceEnsureMergesPreviousWeightIntoExistingDestination(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	for _, name := range []string{"a", "b"} {
		_, err := svc.Client.CoreV1().Services("default").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	ensure := func(service string) {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: service, Namespace: "default"}}},
		}))
	}
	ensure("a")

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualService.Spec.Http[0].Route[0].Weight = 60
	virtualService.Spec.Http[0].Route = append(virtualService.Spec.Http[0].Route,
		&apiNetworking.HTTPRouteDestination{Destination: &apiNetworking.Destination{Host: "b"}, Weight: 20},
		&apiNetworking.HTTPRouteDestination{Destination: &apiNetworking.Destination{Host: "other"}, Weight: 20},
	)
	_, err = client.NetworkingV1beta1().VirtualServices("default").Update(ctx, virtualService, metav1.UpdateOptions{})
	require.NoError(t, err)

	ensure("b")
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	route := virtualService.Spec.Http[0].Route
	require.Len(t, route, 2)
	assert.Equal(t, "b", route[0].Destination.Host)
	assert.Equal(t, int32(80), route[0].Weight)
	assert.Equal(t, "other", route[1].Destination.Host)
	assert.Equal(t, int32(20), route[1].Weight)
}

func TestIstioVirtualServiceEnsureKeepsLocalDestinationWithSameNameAsCrossNamespaceBackend(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	_, err := svc.Client.CoreV1().Services("remote").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web"}}, metav1.CreateOptions{})
	require.NoError(t, err)
	ensure := func() {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "web", Namespace: "remote"}}},
		}))
	}
	ensure()

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualService.Spec.Http[0].Route[0].Weight = 70
	virtualService.Spec.Http[0].Route = append(virtualService.Spec.Http[0].Route,
		&apiNetworking.HTTPRouteDestination{Destination: &apiNetworking.Destination{Host: "web"}, Weight: 30},
	)
	virtualService.Spec.Hosts = append(virtualService.Spec.Hosts, "web")
	_, err = client.NetworkingV1beta1().VirtualServices("default").Update(ctx, virtualService, metav1.UpdateOptions{})
	require.NoError(t, err)

	ensure()
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	route := virtualService.Spec.Http[0].Route
	require.Len(t, route, 2)
	assert.Equal(t, "web.remote.svc.cluster.local", route[0].Destination.Host)
	assert.Equal(t, int32(70), route[0].Weight)
	assert.Equal(t, "web", route[1].Destination.Host)
	assert.Equal(t, int32(30), route[1].Weight)
	assert.Contains(t, virtualService.Spec.Hosts, "web")
}

func TestIstioVirtualServiceEnsureOverrideKeepsUnrecordedConfiguredGateway(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Gateways: []string{"mesh", "shared-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts:     router.Opts{GatewayName: "request-gateway", GatewayNamespace: "edge"},
		Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
	})
	require.NoError(t, err)
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "shared-gateway", "edge/request-gateway"}, virtualService.Spec.Gateways)
	assert.Equal(t, "edge/request-gateway", virtualService.Annotations[gatewayReferenceAnnotation])
}

func TestIstioVirtualServiceRemoveKeepsUnrecordedGateway(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Gateways: []string{"mesh", "shared-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(t, svc.Remove(ctx, idForApp("myapp")))
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "shared-gateway"}, virtualService.Spec.Gateways)
	for _, action := range client.Actions() {
		assert.NotEqual(t, "update", action.GetVerb(), "Remove must not touch a VirtualService this mode never attached")
	}
}

func TestIstioVirtualServiceEnsureRejectsInvalidGatewayReference(t *testing.T) {
	tests := []router.Opts{
		{GatewayName: "kube-system/admin-gateway"},
		{GatewayName: "request-gateway", GatewayNamespace: "edge/other"},
		{GatewayName: "Bad_Name"},
	}
	for _, opts := range tests {
		svc, client := fakeVirtualServiceService()
		require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
		err := svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Opts:     opts,
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
		})
		require.Error(t, err, "%+v", opts)
		assert.Contains(t, err.Error(), "invalid gateway")
		assert.Empty(t, client.Actions(), "invalid Gateway reference must fail before calling Istio")
	}
}

func TestIstioVirtualServiceEnsureKeepsDeterministicOrder(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	for _, name := range []string{"service-a", "service-b"} {
		_, err := svc.Client.CoreV1().Services("default").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	ensure := func(service, gateway string) *istionetworking.VirtualService {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Opts:     router.Opts{GatewayName: gateway, GatewayNamespace: "edge"},
			CNames:   []string{"b.example.com", "a.example.com", "c.example.com"},
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: service, Namespace: "default"}}},
		}))
		virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
		require.NoError(t, err)
		return virtualService
	}
	for i := 0; i < 20; i++ {
		ensure("service-a", "gateway-a")
		virtualService := ensure("service-b", "gateway-b")
		assert.Equal(t, []string{"edge/gateway-b", "mesh"}, virtualService.Spec.Gateways)
		assert.Equal(t, []string{"a.example.com", "b.example.com", "c.example.com", "myapp.my.domain", "service-b"}, virtualService.Spec.Hosts)
	}
}

func TestIstioVirtualServiceEnsureFindsManagedRuleAfterPrependedRule(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	for _, name := range []string{"service-a", "service-b"} {
		_, err := svc.Client.CoreV1().Services("default").Create(ctx, &v1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	ensure := func(service string) {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: service, Namespace: "default"}}},
		}))
	}
	ensure("service-a")

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	pathRule := &apiNetworking.HTTPRoute{
		Match: []*apiNetworking.HTTPMatchRequest{{Uri: &apiNetworking.StringMatch{MatchType: &apiNetworking.StringMatch_Prefix{Prefix: "/api"}}}},
		Route: []*apiNetworking.HTTPRouteDestination{{Destination: &apiNetworking.Destination{Host: "api-backend"}}},
	}
	virtualService.Spec.Http = append([]*apiNetworking.HTTPRoute{pathRule}, virtualService.Spec.Http...)
	_, err = client.NetworkingV1beta1().VirtualServices("default").Update(ctx, virtualService, metav1.UpdateOptions{})
	require.NoError(t, err)

	ensure("service-b")
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, virtualService.Spec.Http, 2)
	require.Len(t, virtualService.Spec.Http[0].Route, 1)
	assert.Equal(t, "api-backend", virtualService.Spec.Http[0].Route[0].Destination.Host)
	require.Len(t, virtualService.Spec.Http[1].Route, 1)
	assert.Equal(t, "service-b", virtualService.Spec.Http[1].Route[0].Destination.Host)
}

func TestIstioVirtualServiceEnsureAppendsCatchAllAfterMatchRules(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	pathRule := &apiNetworking.HTTPRoute{
		Match: []*apiNetworking.HTTPMatchRequest{{Uri: &apiNetworking.StringMatch{MatchType: &apiNetworking.StringMatch_Prefix{Prefix: "/api"}}}},
		Route: []*apiNetworking.HTTPRouteDestination{{Destination: &apiNetworking.Destination{Host: "api-backend"}}},
	}
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Http: []*apiNetworking.HTTPRoute{pathRule}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
	}))
	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, virtualService.Spec.Http, 2)
	assert.Equal(t, "api-backend", virtualService.Spec.Http[0].Route[0].Destination.Host)
	assert.Empty(t, virtualService.Spec.Http[1].Match)
	assert.Equal(t, "myapp-web", virtualService.Spec.Http[1].Route[0].Destination.Host)
}

func TestIstioVirtualServiceEnsureKeepsDestinationPortAndSubset(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	ensure := func() {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
		}))
	}
	ensure()

	virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	virtualService.Spec.Http[0].Route[0].Destination.Port = &apiNetworking.PortSelector{Number: 8080}
	virtualService.Spec.Http[0].Route[0].Destination.Subset = "canary"
	_, err = client.NetworkingV1beta1().VirtualServices("default").Update(ctx, virtualService, metav1.UpdateOptions{})
	require.NoError(t, err)

	ensure()
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	dst := virtualService.Spec.Http[0].Route[0].Destination
	assert.Equal(t, "myapp-web", dst.Host)
	require.NotNil(t, dst.Port)
	assert.Equal(t, uint32(8080), dst.Port.Number)
	assert.Equal(t, "canary", dst.Subset)
}

func TestIstioVirtualServiceEnsureDoesNotClaimPreexistingGateway(t *testing.T) {
	svc, client := fakeVirtualServiceService()
	require.NoError(t, createAppWebService(svc.Client, svc.Namespace, "myapp"))
	_, err := client.NetworkingV1beta1().VirtualServices("default").Create(ctx, &istionetworking.VirtualService{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Gateways: []string{"mesh", "shared-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	ensure := func(opts router.Opts) *istionetworking.VirtualService {
		t.Helper()
		require.NoError(t, svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
			Opts:     opts,
			Prefixes: []router.BackendPrefix{{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}}},
		}))
		virtualService, err := client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
		require.NoError(t, err)
		return virtualService
	}

	virtualService := ensure(router.Opts{})
	assert.NotContains(t, virtualService.Annotations, gatewayReferenceAnnotation)

	virtualService = ensure(router.Opts{GatewayName: "request-gateway", GatewayNamespace: "edge"})
	assert.ElementsMatch(t, []string{"mesh", "shared-gateway", "edge/request-gateway"}, virtualService.Spec.Gateways)
	assert.Equal(t, "edge/request-gateway", virtualService.Annotations[gatewayReferenceAnnotation])

	require.NoError(t, svc.Remove(ctx, idForApp("myapp")))
	virtualService, err = client.NetworkingV1beta1().VirtualServices("default").Get(ctx, "myapp", metav1.GetOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"mesh", "shared-gateway"}, virtualService.Spec.Gateways)
}
