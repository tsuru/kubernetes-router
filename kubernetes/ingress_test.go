// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	networkingV1 "k8s.io/api/networking/v1"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	certmanagerv1clientset "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	fakecertmanager "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned/fake"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func createFakeService(useIngressClassName bool) IngressService {
	client := fake.NewSimpleClientset()
	err := createAppWebService(client, "default", "test")
	if err != nil {
		panic(err)
	}

	return IngressService{
		UseIngressClassName: useIngressClassName,
		DomainSuffix:        "mycloud.com",
		BaseService: &BaseService{
			Namespace:         "default",
			Client:            client,
			TsuruClient:       faketsuru.NewSimpleClientset(),
			ExtensionsClient:  fakeapiextensions.NewSimpleClientset(),
			CertManagerClient: fakecertmanager.NewSimpleClientset(),
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

func createCertManagerIssuer(client certmanagerv1clientset.Interface, namespace, name string) error {
	_, err := client.CertmanagerV1().Issuers(namespace).Create(
		context.TODO(),
		&certmanagerv1.Issuer{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
		metav1.CreateOptions{},
	)

	return err
}

func createCertManagerClusterIssuer(client certmanagerv1clientset.Interface, name string) error {
	_, err := client.CertmanagerV1().ClusterIssuers().Create(
		context.TODO(),
		&certmanagerv1.ClusterIssuer{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		},
		metav1.CreateOptions{},
	)

	return err
}

func TestSecretName(t *testing.T) {
	svc := createFakeService(false)
	appName := "tsuru-dashboard"
	certName := "bigerdomain1234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901.cloud.evenbiiiiiiiiigerrrrr.com"
	sName := svc.secretName(idForApp(appName), certName)
	assert.Equal(t, "kr-tsuru-dashboard-bigerdomain12345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456-237e76c831bb200d", sName)
	appName = "tsuru-dashboard"
	certName = "domain.com"
	sName = svc.secretName(idForApp(appName), certName)
	assert.Equal(t, "kr-tsuru-dashboard-domain.com", sName)
	svc2 := createFakeService(false)
	appName = "tsuru-dashboard"
	certName = "domain.com"
	sName = svc2.secretName(router.InstanceID{AppName: appName, InstanceName: "custom1"}, certName)
	assert.Equal(t, "kr-tsuru-dashboard-domain.com-custom1", sName)
}

func TestIngressEnsure(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{},
		Team: "default",
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
	ingressFound, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
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
			{
				Prefix: "my_process.process",
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})
	require.NoError(t, err)
	ingressFound, err := ingressService.Client.NetworkingV1().Ingresses("default").Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	pathType := networkingV1.PathTypeImplementationSpecific
	expectedIngressRules := []networkingV1.IngressRule{
		{
			Host: "test" + ".",
			IngressRuleValue: networkingV1.IngressRuleValue{
				HTTP: &networkingV1.HTTPIngressRuleValue{
					Paths: []networkingV1.HTTPIngressPath{
						{
							Path:     "",
							PathType: &pathType,
							Backend: networkingV1.IngressBackend{
								Service: &networkingV1.IngressServiceBackend{
									Name: "test-web",
									Port: networkingV1.ServiceBackendPort{
										Number: defaultServicePort,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			Host: "my-process." + "process." + "test.",
			IngressRuleValue: networkingV1.IngressRuleValue{
				HTTP: &networkingV1.HTTPIngressRuleValue{
					Paths: []networkingV1.HTTPIngressPath{
						{
							Path:     "",
							PathType: &pathType,
							Backend: networkingV1.IngressBackend{
								Service: &networkingV1.IngressServiceBackend{
									Name: "test-web",
									Port: networkingV1.ServiceBackendPort{
										Number: defaultServicePort,
									},
								},
							},
						},
					},
				},
			},
		},
		{
			Host: "v1." + "version." + "test.",
			IngressRuleValue: networkingV1.IngressRuleValue{
				HTTP: &networkingV1.HTTPIngressRuleValue{
					Paths: []networkingV1.HTTPIngressPath{
						{
							Path:     "",
							PathType: &pathType,
							Backend: networkingV1.IngressBackend{
								Service: &networkingV1.IngressServiceBackend{
									Name: "test-web-v1",
									Port: networkingV1.ServiceBackendPort{
										Number: defaultServicePort,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	assert.ElementsMatch(t, expectedIngressRules, ingressFound.Spec.Rules)
}

func TestIngressEnsureWithMultipleBackendsWithTLS(t *testing.T) {
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
			Acme:              true,
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
	ingressFound, err := ingressService.Client.NetworkingV1().Ingresses("default").Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngressTLS := []networkingV1.IngressTLS{
		{
			Hosts:      []string{"test."},
			SecretName: "kr-test-test.",
		},
		{
			Hosts:      []string{"v1.version.test."},
			SecretName: "kr-test-v1.version.test.",
		},
	}

	assert.ElementsMatch(t, expectedIngressTLS, ingressFound.Spec.TLS)
}

func TestIngressEnsureWithCNames(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Route: "/admin",
			AdditionalOpts: map[string]string{
				"tsuru.io/some-annotation":       "true",
				"cert-manager.io/cluster-issuer": "letsencrypt-prod",
			},
		},
		CNames: []string{"test.io", "www.test.io"},
		Team:   "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	pathType := networkingV1.PathTypeImplementationSpecific

	expectedIngress.Spec.Rules[0].HTTP.Paths[0].Path = "/admin"
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io,www.test.io"
	expectedIngress.Annotations["tsuru.io/some-annotation"] = "true"

	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"
	delete(expectedIngress.Annotations, "router.tsuru.io/cnames")
	delete(expectedIngress.Annotations, "cert-manager.io/cluster-issuer") // cert-manager.io/cluster-issuer is not allowed on cname ingress when acme is disabled

	expectedIngress.Spec.Rules[0] = networkingV1.IngressRule{
		Host: "test.io",
		IngressRuleValue: networkingV1.IngressRuleValue{
			HTTP: &networkingV1.HTTPIngressRuleValue{
				Paths: []networkingV1.HTTPIngressPath{
					{
						Path:     "/admin",
						PathType: &pathType,
						Backend: networkingV1.IngressBackend{
							Service: &networkingV1.IngressServiceBackend{
								Name: "test-web",
								Port: networkingV1.ServiceBackendPort{
									Number: defaultServicePort,
								},
							},
						},
					},
				},
			},
		},
	}

	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-www.test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress.Name = "kubernetes-router-cname-www.test.io"
	expectedIngress.Spec.Rules[0] = networkingV1.IngressRule{
		Host: "www.test.io",
		IngressRuleValue: networkingV1.IngressRuleValue{
			HTTP: &networkingV1.HTTPIngressRuleValue{
				Paths: []networkingV1.HTTPIngressPath{
					{
						Path:     "/admin",
						PathType: &pathType,
						Backend: networkingV1.IngressBackend{
							Service: &networkingV1.IngressServiceBackend{
								Name: "test-web",
								Port: networkingV1.ServiceBackendPort{
									Number: defaultServicePort,
								},
							},
						},
					},
				},
			},
		},
	}
	delete(expectedIngress.Annotations, "cert-manager.io/cluster-issuer") // cert-manager.io/cluster-issuer is not allowed on cname ingress
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

	_, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-www.test.io", metav1.GetOptions{})
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

	_, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.True(t, k8sErrors.IsNotFound(err))

	foundIngress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, foundIngress.Annotations[AnnotationsCNames], "")
}

func TestIngressEnsureWithTags(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Route: "/admin",
			AdditionalOpts: map[string]string{
				"tsuru.io/some-annotation":       "true",
				"cert-manager.io/cluster-issuer": "letsencrypt-prod",
			},
		},
		Tags: []string{"test.io", "product=myproduct"},
		Team: "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")

	expectedIngress.Spec.Rules[0].HTTP.Paths[0].Path = "/admin"
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Labels["tsuru.io/custom-tag-product"] = "myproduct"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["tsuru.io/some-annotation"] = "true"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestEnsureCertManagerIssuer(t *testing.T) {
	svc := createFakeService(false)

	createCertManagerIssuer(svc.CertManagerClient, svc.Namespace, "letsencrypt")
	createCertManagerClusterIssuer(svc.CertManagerClient, "letsencrypt-cluster")

	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
		},
		CNames: []string{"test.io", "www.test.io"},
		CertIssuers: map[string]string{
			"test.io":     "letsencrypt",
			"www.test.io": "letsencrypt-cluster",
		},
		Team: "default",
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

	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	foundIngress2, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-www.test.io", metav1.GetOptions{})
	require.NoError(t, err)

	assert.Equal(t, foundIngress.Annotations[certManagerCommonName], "test.io")
	assert.Equal(t, foundIngress.Annotations[certManagerIssuerKey], "letsencrypt")

	assert.Equal(t, foundIngress2.Annotations[certManagerCommonName], "www.test.io")
	assert.Equal(t, foundIngress2.Annotations[certManagerClusterIssuerKey], "letsencrypt-cluster")
}

func TestEnsureCertManagerIssuerNotFound(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
		},
		CNames: []string{"test.io", "www.test.io"},
		CertIssuers: map[string]string{
			"test.io":     "letsencrypt",
			"www.test.io": "letsencrypt",
		},
		Team: "default",
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-web",
					Namespace: "default",
				},
			},
		},
	})

	// cert-manager issuer not found
	assert.Error(t, err)
	assert.ErrorContains(t, err, fmt.Sprintf(errIssuerNotFound, "letsencrypt"))
}

func TestIngressCreateDefaultClass(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.IngressClass = "nginx"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{"my-opt": "v1"},
		},
		Team: "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["kubernetes.io/ingress.class"] = "nginx"
	expectedIngress.Annotations["my-opt"] = "v1"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureDefaultClassOverride(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.IngressClass = "nginx"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{"class": "xyz"},
		},
		Team: "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["kubernetes.io/ingress.class"] = "xyz"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureIngressClassName(t *testing.T) {
	svc := createFakeService(true)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.IngressClass = "nginx"
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Team: "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"

	expectedIngress.Spec.IngressClassName = &svc.IngressClass

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureDefaultPrefix(t *testing.T) {
	svc := createFakeService(false)
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
		Team: "default",
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

	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"
	expectedIngress.Annotations["my.prefix.com/foo1"] = "xyz"
	expectedIngress.Annotations["prefixed/foo2"] = "abc"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressEnsureRemoveAnnotation(t *testing.T) {
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				"ann1-": "",
			},
		},
		Team: "default",
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

	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["ann2"] = "val2"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestIngressCreateDefaultPort(t *testing.T) {
	svc := createFakeService(false)
	err := createCRD(svc.BaseService, "myapp", "custom-namespace", nil)
	require.NoError(t, err)
	err = createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("create", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		newIng, ok := action.(ktesting.UpdateAction).GetObject().(*networkingV1.Ingress)
		if !ok {
			t.Errorf("Error creating ingress.")
		}
		port := newIng.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number
		require.Equal(t, int32(8888), port)
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
	svc := createFakeService(false)
	svcName := "test"
	svcPort := 8000
	resourceVersion := "123"
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("get", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		ingress := &networkingV1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:            svcName,
				ResourceVersion: resourceVersion,
			},
			Spec: networkingV1.IngressSpec{
				DefaultBackend: &networkingV1.IngressBackend{
					Service: &networkingV1.IngressServiceBackend{
						Name: svcName,
						Port: networkingV1.ServiceBackendPort{Number: int32(svcPort)},
					},
				},
			},
		}
		return true, ingress, nil
	})
	svc.BaseService.Client.(*fake.Clientset).PrependReactor("update", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		newIng, ok := action.(ktesting.UpdateAction).GetObject().(*networkingV1.Ingress)
		if !ok {
			t.Fatalf("Error updating ingress.")
		}
		if newIng.ObjectMeta.ResourceVersion != resourceVersion {
			t.Errorf("Expected ResourceVersion %q. Got %s", resourceVersion, newIng.ObjectMeta.ResourceVersion)
		}
		if newIng.Spec.DefaultBackend == nil || newIng.Spec.DefaultBackend.Service.Name != svcName || newIng.Spec.DefaultBackend.Service.Port.Number != int32(svcPort) {
			t.Errorf("Expected Backend with name %q and port %d. Got %v", svcName, svcPort, newIng.Spec.DefaultBackend)
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

func TestEnsureExistingIngressWithFreeze(t *testing.T) {
	svc := createFakeService(false)
	svcName := "test"
	svcPort := 8000
	resourceVersion := "123"
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("get", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		ingress := &networkingV1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:            svcName,
				ResourceVersion: resourceVersion,
				Annotations: map[string]string{
					AnnotationFreeze: "true",
				},
			},
			Spec: networkingV1.IngressSpec{
				DefaultBackend: &networkingV1.IngressBackend{
					Service: &networkingV1.IngressServiceBackend{
						Name: svcName,
						Port: networkingV1.ServiceBackendPort{
							Number: int32(svcPort),
						},
					},
				},
			},
		}
		return true, ingress, nil
	})

	called := false
	svc.BaseService.Client.(*fake.Clientset).PrependReactor("update", "ingresses", func(action ktesting.Action) (bool, runtime.Object, error) {
		called = true
		return true, nil, errors.New("must never called")
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
	require.False(t, called)
}

func TestEnsureIngressAppNamespace(t *testing.T) {
	svc := createFakeService(false)
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

	ingressList, err := svc.Client.NetworkingV1().Ingresses("custom-namespace").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	assert.Len(t, ingressList.Items, 1)
}

func TestIngressGetAddress(t *testing.T) {
	svc := createFakeService(false)
	svc.DomainSuffix = "apps.example.org"
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
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
	svc := createFakeService(false)
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.HTTPPort = 8888
	svc.DomainSuffix = "apps.example.org"
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{

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
	svc := createFakeService(false)
	svc.DomainSuffix = "" // cleaning the precedence of domainSuffix
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
	assert.Equal(t, []string{"https://test.apps.example.org"}, addrs)
}
func TestIngressGetAddressTLS(t *testing.T) {
	svc := createFakeService(false)
	svc.DomainSuffix = "" // cleaning the precedence of domainSuffix
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
			{
				Prefix: "my_process.process",
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
	assert.ElementsMatch(t, []string{"https://my-process.process.test.apps.example.org", "https://v1.version.test.apps.example.org", "https://test.apps.example.org"}, addrs)
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
			svc := createFakeService(false)

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

			ingressList, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).List(ctx, metav1.ListOptions{})
			require.NoError(t, err)

			assert.Len(t, ingressList.Items, tc.expectedCount)
		})
	}
}

func TestRemoveCertificate(t *testing.T) {
	svc := createFakeService(false)
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
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", expectedCert)
	require.NoError(t, err)
	err = svc.RemoveCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com")
	require.NoError(t, err)
}

func TestRemoveCertificateACMEHandled(t *testing.T) {
	svc := createFakeService(false)
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
		},
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
	err = svc.RemoveCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com")
	assert.EqualError(t, err, "cannot remove certificate from ingress kubernetes-router-test-blue-ingress, it is managed by ACME")
}

func TestAddCertificate(t *testing.T) {
	svc := createFakeService(false)
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
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", expectedCert)
	require.NoError(t, err)

	certTest := defaultIngress("test-blue.mycloud.com", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]networkingV1.IngressTLS{
			{
				Hosts:      []string{"test-blue.mycloud.com"},
				SecretName: svc.secretName(idForApp("test-blue"), "test-blue.mycloud.com"),
			},
		}...)

	ingress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)
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
	ingress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)
}

func TestAddCertificateACMEHandled(t *testing.T) {
	svc := createFakeService(false)
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
		},
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
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", expectedCert)
	assert.EqualError(t, err, "cannot add certificate to ingress kubernetes-router-test-blue-ingress, it is managed by ACME")

}

func TestAddCertificateWithOverride(t *testing.T) {
	svc := createFakeService(false)
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
	firstCert := router.CertData{Certificate: "FirstCert", Key: "FirstKey"}
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", firstCert)
	require.NoError(t, err)

	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", expectedCert)
	require.NoError(t, err)

	secretName := svc.secretName(idForApp("test-blue"), "test-blue.mycloud.com")
	certTest := defaultIngress("test-blue.mycloud.com", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]networkingV1.IngressTLS{
			{
				Hosts:      []string{"test-blue.mycloud.com"},
				SecretName: secretName,
			},
		}...)

	ingress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)

	secret, err := svc.Client.CoreV1().Secrets(svc.Namespace).Get(ctx, secretName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, expectedCert.Certificate, secret.StringData["tls.crt"])
	assert.Equal(t, expectedCert.Key, secret.StringData["tls.key"])
}

func TestAddCertificateWithCName(t *testing.T) {
	svc := createFakeService(false)
	err := createAppWebService(svc.Client, svc.Namespace, "test-blue")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Team: "default",
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-blue-web",
					Namespace: svc.Namespace,
				},
			},
		},
		CNames: []string{"mydomain.com"},
	})
	require.NoError(t, err)

	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "mydomain.com", expectedCert)
	require.NoError(t, err)

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]networkingV1.IngressTLS{
			{
				Hosts:      []string{"mydomain.com"},
				SecretName: svc.secretName(idForApp("test-blue"), "mydomain.com"),
			},
		}...)

	ingress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-mydomain.com", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)

	err = svc.Ensure(ctx, idForApp("test-blue"), router.EnsureBackendOpts{
		Team: "default",
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "test-blue-web",
					Namespace: svc.Namespace,
				},
			},
		},
		CNames: []string{"mydomain.com"},
	})
	require.NoError(t, err)

	ingress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-mydomain.com", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)

	// Remove the cert
	err = svc.RemoveCertificate(ctx, idForApp("test-blue"), "mydomain.com")
	require.NoError(t, err)
	ingress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-mydomain.com", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, ingress.Spec.TLS, 0)
}

func TestGetCertificate(t *testing.T) {
	svc := createFakeService(false)
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
	err = svc.AddCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com", expectedCert)
	require.NoError(t, err)

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]networkingV1.IngressTLS{
			{
				Hosts:      []string{"test-blue.mycloud.com"},
				SecretName: svc.secretName(idForApp("test-blue"), "test-blue.mycloud.com"),
			},
		}...)

	ingress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-blue-ingress", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, certTest.Spec.TLS, ingress.Spec.TLS)

	cert, err := svc.GetCertificate(ctx, idForApp("test-blue"), "test-blue.mycloud.com")
	require.NoError(t, err)
	assert.Equal(t, &router.CertData{Certificate: "", Key: ""}, cert)
}

func TestEnsureWithTLSAndCName(t *testing.T) {
	svc := createFakeService(false)
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme: true,
			AdditionalOpts: map[string]string{
				"cert-manager.io/cluster-issuer": "letsencrypt-prod",
			},
		},
		Team:   "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"
	expectedIngress.Annotations["cert-manager.io/cluster-issuer"] = "letsencrypt-prod"
	expectedIngress.Spec.TLS = []networkingV1.IngressTLS{
		{
			Hosts:      []string{"test.mycloud.com"},
			SecretName: "kr-test-test.mycloud.com",
		},
	}
	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress = defaultIngress("test", "default")
	expectedIngress.Spec.Rules[0].Host = "test.io"
	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"

	assert.Equal(t, expectedIngress, foundIngress)
}

func TestEnsureWithTLSAndCNameAndAcmeCName(t *testing.T) {
	svc := createFakeService(false)
	err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Acme:      true,
			AcmeCName: true,
		},
		Team:   "default",
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
	foundIngress, err := svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-test-ingress", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Annotations["router.tsuru.io/cnames"] = "test.io"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"
	expectedIngress.Spec.TLS = []networkingV1.IngressTLS{
		{
			Hosts:      []string{"test.mycloud.com"},
			SecretName: "kr-test-test.mycloud.com",
		},
	}
	assert.Equal(t, expectedIngress, foundIngress)

	foundIngress, err = svc.Client.NetworkingV1().Ingresses(svc.Namespace).Get(ctx, "kubernetes-router-cname-test.io", metav1.GetOptions{})
	require.NoError(t, err)

	expectedIngress = defaultIngress("test", "default")
	expectedIngress.Spec.Rules[0].Host = "test.io"
	expectedIngress.Name = "kubernetes-router-cname-test.io"
	expectedIngress.Labels["tsuru.io/app-name"] = "test"
	expectedIngress.Labels["tsuru.io/app-team"] = "default"
	expectedIngress.Labels["router.tsuru.io/is-cname-ingress"] = "true"
	expectedIngress.Annotations["kubernetes.io/tls-acme"] = "true"

	expectedIngress.Spec.TLS = []networkingV1.IngressTLS{
		{
			Hosts:      []string{"test.io"},
			SecretName: "kr-test-test.io",
		},
	}

	assert.Equal(t, expectedIngress, foundIngress)
}

func defaultIngress(name, namespace string) *networkingV1.Ingress {
	serviceName := name + "-web"
	blockOwnerDeletion := true
	controller := true
	pathType := networkingV1.PathTypeImplementationSpecific

	return &networkingV1.Ingress{
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
		Spec: networkingV1.IngressSpec{
			Rules: []networkingV1.IngressRule{
				{
					Host: name + ".mycloud.com",
					IngressRuleValue: networkingV1.IngressRuleValue{
						HTTP: &networkingV1.HTTPIngressRuleValue{
							Paths: []networkingV1.HTTPIngressPath{
								{
									Path:     "",
									PathType: &pathType,
									Backend: networkingV1.IngressBackend{
										Service: &networkingV1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingV1.ServiceBackendPort{
												Number: defaultServicePort,
											},
										},
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
