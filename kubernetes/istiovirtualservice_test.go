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
		ObjectMeta: metav1.ObjectMeta{Name: "myapp"},
		Spec:       apiNetworking.VirtualService{Gateways: []string{"mesh", "edge/shared-gateway", "other-gateway"}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	svc.GatewayNamespace = "edge"

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
