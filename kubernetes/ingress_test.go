// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func createFakeService() IngressService {
	client := fake.NewSimpleClientset()
	err := createAppWebService(client, "default", "test")
	if err != nil {
		panic(err)
	}

	return IngressService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           client,
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
	}
}

func createAppWebService(client kubernetes.Interface, namespace, appName string) error {
	_, err := client.CoreV1().Services(namespace).Create(context.TODO(), &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: appName + "-web",
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"tsuru.io/app-name":    appName,
				"tsuru.io/app-process": "web",
			},
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       defaultServicePort,
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}, metav1.CreateOptions{})

	return err
}

func TestSecretName(t *testing.T) {
	svc := createFakeService()
	appName := "tsuru-dashboard"
	certName := "bigerdomain1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901.cloud.evenbiiiiiiiiigerrrrr.com"
	sName := svc.secretName(idForApp(appName), certName)
	assert.Equal(t, "kr-tsuru-dashboard-bigerdomain12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-237e76c831bb200d", sName)
	appName = "tsuru-dashboard"
	certName = "domain.com"
	sName = svc.secretName(idForApp(appName), certName)
	assert.Equal(t, "kr-tsuru-dashboard-domain.com", sName)
	svc2 := createFakeService()
	appName = "tsuru-dashboard"
	certName = "domain.com"
	sName = svc2.secretName(router.InstanceID{AppName: appName, InstanceName: "custom1"}, certName)
	assert.Equal(t, "kr-tsuru-dashboard-domain.com-custom1", sName)
}

func TestIngressEnsure(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	ingressFound, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"

	assert.Equal(t, expectedIngress, ingressFound)
}

func TestIngressEnsureWithMultipleBackends(t *testing.T) {
	client := fake.NewSimpleClientset()
	err := createAppWebService(client, "default", "test")
	require.NoError(t, err)
	_, err = client.CoreV1().Services("default").Create(context.TODO(), &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test" + "-web" + "-v1",
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"tsuru.io/app-name":    "test",
				"tsuru.io/app-process": "web",
			},
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       defaultServicePort,
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	ingressService := IngressService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           client,
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
	}

	ingressService.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	ingressService.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err = ingressService.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			ExposeAllServices: true,
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
			{
				Prefix: "v1.version",
				Target: router.BackendTarget{
					Service:   "test-web-v1",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	ingressFound, err := ingressService.Client.ExtensionsV1beta1().Ingresses("default").Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	pathType := v1beta1.PathTypeImplementationSpecific
	expectedIngressRules := []v1beta1.IngressRule{
		{
			Host: "test" + ".",
			IngressRuleValue: v1beta1.IngressRuleValue{
				HTTP: &v1beta1.HTTPIngressRuleValue{
					Paths: []v1beta1.HTTPIngressPath{
						{
							Path:     "",
							PathType: &pathType,
							Backend: v1beta1.IngressBackend{
								ServiceName: "test-web",
								ServicePort: intstr.FromInt(defaultServicePort),
							},
						},
					},
				},
			},
		},
		{
			Host: "v1." + "version." + "test.",
			IngressRuleValue: v1beta1.IngressRuleValue{
				HTTP: &v1beta1.HTTPIngressRuleValue{
					Paths: []v1beta1.HTTPIngressPath{
						{
							Path:     "",
							PathType: &pathType,
							Backend: v1beta1.IngressBackend{
								ServiceName: "test-web-v1",
								ServicePort: intstr.FromInt(defaultServicePort),
							},
						},
					},
				},
			},
		},
	}

	assert.ElementsMatch(t, expectedIngressRules, ingressFound.Spec.Rules)
}

func TestIngressEnsureWithCNames(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Route: "/admin",
		},
		CNames: []string{"test.io", "www.test.io"},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
			{
				Prefix: "subscriber",
				Target: router.BackendTarget{
					Service:   "test-subscriber",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	pathType := v1beta1.PathTypeImplementationSpecific

	expectedIngress.Spec.Rules[0].HTTP.Paths[0].Path = "/admin"
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io,www.test.io"

	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"
	delete(expectedIngress.Annotations, "router.tsuru.io/cnames")

	expectedIngress.Spec.Rules[0] = v1beta1.IngressRule{
		Host: "test.io",
		IngressRuleValue: v1beta1.IngressRuleValue{
			HTTP: &v1beta1.HTTPIngressRuleValue{
				Paths: []v1beta1.HTTPIngressPath{
					{
						Path:     "/admin",
						PathType: &pathType,
						Backend: v1beta1.IngressBackend{
							ServiceName: "test-web",
							ServicePort: intstr.FromInt(8888),
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-www.test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress.Name = "kubernetes-router-cname-www.test.io"
	expectedIngress.Spec.Rules[0] = v1beta1.IngressRule{
		Host: "www.test.io",
		IngressRuleValue: v1beta1.IngressRuleValue{
			HTTP: &v1beta1.HTTPIngressRuleValue{
				Paths: []v1beta1.HTTPIngressPath{
					{
						Path:     "/admin",
						PathType: &pathType,
						Backend: v1beta1.IngressBackend{
							ServiceName: "test-web",
							ServicePort: intstr.FromInt(8888),
						},
					},
				},
			},
		},
	}
	assert.Equal(t, expectedIngress, foundIngress)

	// test removing www.test.io
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Route: "/admin",
		},
		CNames: []string{"test.io"},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
			{
				Prefix: "subscriber",
				Target: router.BackendTarget{
					Service:   "test-subscriber",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-www.test.io", metav1.GetOptions{})
	require.True(t, k8sErrors.IsNotFound(err))

	// test removing all cnames
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Route: "/admin",
		},
		CNames: []string{},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
			{
				Prefix: "subscriber",
				Target: router.BackendTarget{
					Service:   "test-subscriber",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.True(t, k8sErrors.IsNotFound(err))

	foundIngress, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, foundIngress.Annotations[AnnotationsCNames], "")
}

func TestIngressCreateDefaultClass(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.IngressClass = "nginx"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{"my-opt": "v1"},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["kubernetes.io/ingress.class"] = "nginx"
	expectedIngress.Annotations["my-opt"] = "v1"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureDefaultClassOverride(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.IngressClass = "nginx"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{"class": "xyz"},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["kubernetes.io/ingress.class"] = "xyz"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureDefaultPrefix(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.AnnotationsPrefix = "my.prefix.com"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				"foo1":          "xyz",
				"prefixed/foo2": "abc",
			},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["my.prefix.com/foo1"] = "xyz"
	expectedIngress.Annotations["prefixed/foo2"] = "abc"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureRemoveAnnotation(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				"ann1-": "",
			},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann2"] = "val2"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressCreateDefaultPort(t *testing.T) {
	svc := createFakeService()
	err := createCRD(svc.BaseService, "myapp", "custom-namespace", nil)
	require.NoError(t, err)
	err = createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("create", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		newIng, ok := action.(ktesting.UpdateAction).GetObject().(*v1beta1.Ingress)
		if !ok {
			t.Errorf("Error creating ingress.")
		}
		port := newIng.Spec.Rules[0].HTTP.Paths[0].Backend.ServicePort
		require.Equal(t, intstr.FromInt(8888), port)
		return false, nil, nil
	})
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Pool: "mypool",
			AdditionalOpts: map[string]string{
				"my-opt": "value",
			},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "myapp-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
}

func TestEnsureExistingIngress(t *testing.T) {
	svc := createFakeService()
	svcName := "test"
	svcPort := 8000
	resourceVersion := "123"
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("get", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		ingress := &v1beta1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:            svcName,
				ResourceVersion: resourceVersion,
			},
			Spec: v1beta1.IngressSpec{
				Backend: &v1beta1.IngressBackend{
					ServiceName: svcName,
					ServicePort: intstr.FromInt(svcPort),
				},
			},
		}
		return true, ingress, nil
	})
	svc.BaseService.Client.(*fake.Clientset).PrependReactor("update", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		newIng, ok := action.(ktesting.UpdateAction).GetObject().(*v1beta1.Ingress)
		if !ok {
			t.Fatalf("Error updating ingress.")
		}
		if newIng.ObjectMeta.ResourceVersion != resourceVersion {
			t.Errorf("Expected ResourceVersion %q. Got %s", resourceVersion, newIng.ObjectMeta.ResourceVersion)
		}
		if newIng.Spec.Backend == nil || newIng.Spec.Backend.ServiceName != svcName || newIng.Spec.Backend.ServicePort.IntValue() != svcPort {
			t.Errorf("Expected Backend with name %q and port %d. Got %v", svcName, svcPort, newIng.Spec.Backend)
		}
		return true, newIng, nil
	})

	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Pool: "mypool",
			AdditionalOpts: map[string]string{
				"my-opt": "value",
			},
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
}

func TestEnsureIngressAppNamespace(t *testing.T) {
	svc := createFakeService()
	err := createCRD(svc.BaseService, "app", "custom-namespace", nil)
	require.NoError(t, err)
	err = createAppWebService(svc.Client, svc.Namespace, "app")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("app"), router.EnsureBackendOpts{
		Opts: router.Opts{},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "app-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses("custom-namespace").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	assert.Len(t, ingressList.Items, 1)
}

func TestIngressGetAddress(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			DomainSuffix: "apps.example.org",
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	addrs, err := svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"test.apps.example.org"}, addrs)
}
func TestIngressGetAddressWithPort(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.HTTPPort = 8888
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			DomainSuffix: "apps.example.org",
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	addrs, err := svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"test.apps.example.org:8888"}, addrs)
}
func TestIngressGetAddressWithPortTLS(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.HTTPPort = 8888
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			DomainSuffix: "apps.example.org",
			Acme:         true,
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	addrs, err := svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"https://test.apps.example.org:8888"}, addrs)
}
func TestIngressGetAddressTLS(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			DomainSuffix: "apps.example.org",
			Acme:         true,
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	addrs, err := svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"https://test.apps.example.org"}, addrs)
}

func TestIngressGetMultipleAddresses(t *testing.T) {
	client := fake.NewSimpleClientset()
	err := createAppWebService(client, "default", "test")
	require.NoError(t, err)
	_, err = client.CoreV1().Services("default").Create(context.TODO(), &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test" + "-web" + "-v1",
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{
				"tsuru.io/app-name":    "test",
				"tsuru.io/app-process": "web",
			},
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       defaultServicePort,
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	ingressSvc := IngressService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           client,
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
	}
	ingressSvc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	ingressSvc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err = ingressSvc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			DomainSuffix:      "apps.example.org",
			Acme:              true,
			ExposeAllServices: true,
		},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
			{
				Prefix: "v1.version",
				Target: router.BackendTarget{
					Service:   "test-web-v1",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)

	addrs, err := ingressSvc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"https://v1.version.test.apps.example.org", "https://test.apps.example.org"}, addrs)
}

func TestRemove(t *testing.T) {
	tt := []struct {
		testName      string
		remove        string
		expectedErr   error
		expectedCount int
	}{
		{"success", "test", nil, 0},
		{"ignoresNotFound", "notfound", nil, 1},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			svc := createFakeService()

			err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
				Prefixes: []router.BackendPrefix{
					{
						Target: router.BackendTarget{
							Service:   "test-web",
							Namespace: svc.Namespace,
						},
					},
				},
			})
			require.NoError(t, err)

			err = svc.Remove(ctx, idForApp(tc.remove))
			assert.Equal(t, tc.expectedErr, err)

			ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(ctx, metav1.ListOptions{})
			require.NoError(t, err)

			assert.Len(t, ingressList.Items, tc.expectedCount)
		})
	}
}

func TestRemoveCertificate(t *testing.T) {
	svc := createFakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-blue-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "mycert", expectedCert)
	require.NoError(t, err)
	err = svc.RemoveCertificate(ctx, idForApp("test-blue"), "mycert")
	require.NoError(t, err)
}

func TestAddCertificate(t *testing.T) {
	svc := createFakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-blue-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "mycert", expectedCert)
	require.NoError(t, err)

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]v1beta1.IngressTLS{
			{
				Hosts:      []string{"mycert"},
				SecretName: svc.secretName(idForApp("test-blue"), "mycert"),
			},
		}...)

	ingress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)
}

func TestGetCertificate(t *testing.T) {
	svc := createFakeService()
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-blue-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "mycert", expectedCert)
	require.NoError(t, err)

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]v1beta1.IngressTLS{
			{
				Hosts:      []string{"mycert"},
				SecretName: svc.secretName(idForApp("test-blue"), "mycert"),
			},
		}...)

	ingress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)

	cert, err := svc.GetCertificate(ctx, idForApp("test-blue"), "mycert")
	require.NoError(t, err)
	assert.Equal(t, &router.CertData{Certificate: "", Key: ""}, cert)
}

func TestEnsureWithTLSAndCName(t *testing.T) {
	svc := createFakeService()
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
		},
		CNames: []string{"test.io"},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"
	expectedIngress.Spec.TLS = []v1beta1.IngressTLS{
		{
			Hosts:      []string{"test."},
			SecretName: "kr-test-test.",
		},
	}
	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress = defaultIngress("test", "default")
	expectedIngress.Spec.Rules[0].Host = "test.io"
	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestEnsureWithTLSAndCNameAndAcmeCName(t *testing.T) {
	svc := createFakeService()
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme:      true,
			AcmeCName: true,
		},
		CNames: []string{"test.io"},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	foundIngress, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"
	expectedIngress.Spec.TLS = []v1beta1.IngressTLS{
		{
			Hosts:      []string{"test."},
			SecretName: "kr-test-test.",
		},
	}
	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress = defaultIngress("test", "default")
	expectedIngress.Spec.Rules[0].Host = "test.io"
	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"

	expectedIngress.Spec.TLS = []v1beta1.IngressTLS{
		{
			Hosts:      []string{"test.io"},
			SecretName: "kr-test-test.io",
		},
	}

	assert.Equal(t, expectedIngress, foundIngress)
}

func defaultIngress(name, namespace string) *v1beta1.Ingress {
	serviceName := name + "-web"
	blockOwnerDeletion := true
	controller := true
	pathType := v1beta1.PathTypeImplementationSpecific

	return &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubernetes-router-" + name + "-ingress",
			Namespace: namespace,
			Labels: map[string]string{
				appLabel:                     name,
				appBaseServiceNamespaceLabel: namespace,
				appBaseServiceNameLabel:      serviceName,
			},
			Annotations: make(map[string]string),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "v1",
					Kind:               "Service",
					Name:               name + "-web",
					BlockOwnerDeletion: &blockOwnerDeletion,
					Controller:         &controller,
				},
			},
		},
		Spec: v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: name + ".",
					IngressRuleValue: v1beta1.IngressRuleValue{
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath{
								{
									Path:     "",
									PathType: &pathType,
									Backend: v1beta1.IngressBackend{
										ServiceName: serviceName,
										ServicePort: intstr.FromInt(defaultServicePort),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
