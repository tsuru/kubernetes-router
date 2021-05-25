// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

var ctx = context.Background()

func createFakeLBService() LBService {
	return LBService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           fake.NewSimpleClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
		OptsAsLabels:     make(map[string]string),
		OptsAsLabelsDocs: make(map[string]string),
	}
}

func TestLBEnsure(t *testing.T) {
	svc := createFakeLBService()
	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"annotation": "annval"}
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{Pool: "mypool", AdditionalOpts: map[string]string{"my-opt": "value"}, DomainSuffix: "myapps.io"},
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
	setIP(t, svc, "test")
	foundService, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, "test-router-lb", metav1.GetOptions{})
	require.NoError(t, err)

	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["my-opt-as-label"] = "value"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"annotation": "annval",
		"external-dns.alpha.kubernetes.io/hostname": "test.myapps.io",
		"router.tsuru.io/opts":                      `{"Pool":"mypool","DomainSuffix":"myapps.io","AdditionalOpts":{"my-opt":"value"}}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	assert.Equal(t, expectedService, foundService)
}

func TestLBEnsureWithExternalTrafficPolicy(t *testing.T) {
	svc := createFakeLBService()
	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"annotation": "annval"}
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{Pool: "mypool", ExternalTrafficPolicy: "Local", AdditionalOpts: map[string]string{}, DomainSuffix: "myapps.io"},
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
	setIP(t, svc, "test")
	foundService, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, "test-router-lb", metav1.GetOptions{})
	require.NoError(t, err)

	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"annotation": "annval",
		"external-dns.alpha.kubernetes.io/hostname": "test.myapps.io",
		"router.tsuru.io/opts":                      `{"Pool":"mypool","DomainSuffix":"myapps.io","ExternalTrafficPolicy":"Local"}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	expectedService.Spec.ExternalTrafficPolicy = v1.ServiceExternalTrafficPolicyTypeLocal
	assert.Equal(t, expectedService, foundService)
}

func TestLBEnsureWithDomain(t *testing.T) {
	svc := createFakeLBService()
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"annotation": "annval"}
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}

	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Pool:           "mypool",
			AdditionalOpts: map[string]string{"my-opt": "value"},
			Domain:         "myappdomain.zone.io",
		},
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
	setIP(t, svc, "test")
	foundService, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, "test-router-lb", metav1.GetOptions{})
	require.NoError(t, err)
	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["my-opt-as-label"] = "value"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"annotation": "annval",
		"external-dns.alpha.kubernetes.io/hostname": "myappdomain.zone.io",
		"router.tsuru.io/opts":                      `{"Pool":"mypool","Domain":"myappdomain.zone.io","AdditionalOpts":{"my-opt":"value"}}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	assert.Equal(t, expectedService, foundService)
}

func TestLBEnsureCustomAnnotation(t *testing.T) {
	svc := createFakeLBService()
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}

	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			Pool: "mypool",
			AdditionalOpts: map[string]string{
				"my-opt":                 "value",
				"other-opt":              "other-value",
				"svc-annotation-a:b/x:y": "true",
				"ann1-":                  "",
			},
		},
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
	setIP(t, svc, "test")
	foundService, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, "test-router-lb", metav1.GetOptions{})
	require.NoError(t, err)
	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["my-opt-as-label"] = "value"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"ann2":                 "val2",
		"other-opt":            "other-value",
		"a.b/x.y":              "true",
		"router.tsuru.io/opts": `{"Pool":"mypool","AdditionalOpts":{"ann1-":"","my-opt":"value","other-opt":"other-value","svc-annotation-a:b/x:y":"true"}}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	assert.Equal(t, expectedService, foundService)
}

func TestLBEnsureDefaultPort(t *testing.T) {
	svc := createFakeLBService()
	err := createCRD(svc.BaseService, "myapp", "custom-namespace", nil)
	require.NoError(t, err)

	err = createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	svc.BaseService.Client.(*fake.Clientset).PrependReactor("create", "services", func(action ktesting.Action) (bool, runtime.Object, error) {
		newSvc, ok := action.(ktesting.CreateAction).GetObject().(*v1.Service)
		if !ok {
			t.Errorf("Error creating service.")
		}
		ports := newSvc.Spec.Ports
		if len(ports) != 1 || ports[0].TargetPort != intstr.FromInt(8888) {
			t.Errorf("Expected service with targetPort 8888. Got %#v", ports)
		}
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
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)
}

func TestLBSupportedOptions(t *testing.T) {
	svc := createFakeLBService()
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.OptsAsLabels["my-opt2"] = "my-opt-as-label2"
	svc.OptsAsLabelsDocs["my-opt2"] = "User friendly option description."
	options := svc.SupportedOptions(ctx)
	expectedOptions := map[string]string{
		"my-opt2":          "User friendly option description.",
		"exposed-port":     "",
		"my-opt":           "my-opt-as-label",
		"expose-all-ports": "Expose all ports used by application in the Load Balancer. Defaults to false.",
	}
	if !reflect.DeepEqual(options, expectedOptions) {
		t.Errorf("Expected %v. Got %v", expectedOptions, options)
	}
}

func TestLBEnsureAppNamespace(t *testing.T) {
	svc := createFakeLBService()

	err := createAppWebService(svc.Client, svc.Namespace, "app")
	require.NoError(t, err)

	err = createCRD(svc.BaseService, "app", "custom-namespace", nil)
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("app"), router.EnsureBackendOpts{
		Opts: router.Opts{},
		Prefixes: []router.BackendPrefix{
			{
				Target: router.BackendTarget{
					Service:   "app-web",
					Namespace: svc.Namespace,
				},
			},
		},
	})
	require.NoError(t, err)

	serviceList, err := svc.Client.CoreV1().Services("custom-namespace").List(ctx, metav1.ListOptions{})
	require.NoError(t, err)

	if len(serviceList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(serviceList.Items))
	}
}

func TestLBRemove(t *testing.T) {
	tt := []struct {
		testName      string
		remove        string
		expectedErr   error
		expectedCount int
	}{
		{"success", "test", nil, 1},
		{"ignoresNotFound", "notfound", nil, 2},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			svc := createFakeLBService()

			err := createAppWebService(svc.Client, svc.Namespace, "test")
			require.NoError(t, err)

			err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
				Opts: router.Opts{},
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
			setIP(t, svc, "test")

			err = svc.Remove(ctx, idForApp(tc.remove))

			assert.Equal(t, tc.expectedErr, err)
			serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(ctx, metav1.ListOptions{})
			require.NoError(t, err)
			assert.Len(t, serviceList.Items, tc.expectedCount)
		})
	}
}

func TestLBUpdate(t *testing.T) {
	svc1 := v1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:        "test-single",
		Namespace:   "default",
		Labels:      map[string]string{appLabel: "test"},
		Annotations: map[string]string{"test-ann": "val-ann"},
	},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-single"},
			Ports:    []v1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899)}},
		},
	}
	svc2 := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-web",
			Namespace: "default",
			Labels:    map[string]string{appLabel: "test", processLabel: "web", "custom1": "value1"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-web"},
			Ports:    []v1.ServicePort{{Protocol: "TCP", Port: int32(8890), TargetPort: intstr.FromInt(8890)}},
		},
	}
	svc3 := svc2
	svc3.ObjectMeta.Labels = svc1.ObjectMeta.Labels
	svc4 := svc2
	svc4.Spec.Ports = []v1.ServicePort{
		{Protocol: "TCP", Port: int32(8890), TargetPort: intstr.FromInt(8890)},
		{Protocol: "TCP", Port: int32(80), TargetPort: intstr.FromInt(8891)},
	}
	svc5 := svc2
	svc5.Name = "test-web-v1"
	svc5.Labels = map[string]string{appLabel: "test", processLabel: "web", "custom2": "value2"}
	svc5.Spec.Selector = map[string]string{"name": "test-web", "version": "v1"}
	tt := []struct {
		name             string
		services         []v1.Service
		backendTarget    router.BackendTarget
		expectedErr      error
		expectedSelector map[string]string
		expectedPorts    []v1.ServicePort
		expectedLabels   map[string]string
		exposeAllPorts   bool
	}{
		{
			name:        "noServices",
			services:    []v1.Service{},
			expectedErr: ErrNoService{App: "test"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8888),
				},
			},
		},
		{
			name:           "noServices with expose all",
			services:       []v1.Service{},
			exposeAllPorts: true,
			expectedErr:    ErrNoService{App: "test"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8888),
				},
			},
		},
		{
			name:             "singleService with expose all",
			services:         []v1.Service{svc1},
			exposeAllPorts:   true,
			backendTarget:    router.BackendTarget{Service: svc1.Name, Namespace: svc1.Namespace},
			expectedSelector: map[string]string{"name": "test-single"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",

				appBaseServiceNameLabel:      svc1.Name,
				appBaseServiceNamespaceLabel: svc1.Namespace,
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8899),
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       int32(8899),
					TargetPort: intstr.FromInt(8899),
				},
			},
		},
		{
			name:             "singleService",
			services:         []v1.Service{svc1},
			backendTarget:    router.BackendTarget{Service: svc1.Name, Namespace: svc1.Namespace},
			expectedSelector: map[string]string{"name": "test-single"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",

				appBaseServiceNameLabel:      svc1.Name,
				appBaseServiceNamespaceLabel: svc1.Namespace,
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8899),
				},
			},
		},
		{
			name:             "multiServiceWithWeb",
			services:         []v1.Service{svc1, svc2},
			exposeAllPorts:   true,
			backendTarget:    router.BackendTarget{Service: svc2.Name, Namespace: svc2.Namespace},
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				processLabel:         "web",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
				"custom1":            "value1",

				appBaseServiceNameLabel:      svc2.Name,
				appBaseServiceNamespaceLabel: svc2.Namespace,
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8890),
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       int32(8890),
					TargetPort: intstr.FromInt(8890),
				},
			},
		},
		{
			name:        "multiServiceWithoutWeb",
			services:    []v1.Service{svc1, svc3},
			expectedErr: ErrNoService{App: "test"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8888),
				},
			},
		},
		{
			name:             "service with conflicting port, port is ignored",
			services:         []v1.Service{svc4},
			exposeAllPorts:   true,
			backendTarget:    router.BackendTarget{Service: svc4.Name, Namespace: svc4.Namespace},
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				processLabel:         "web",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
				"custom1":            "value1",

				appBaseServiceNameLabel:      svc4.Name,
				appBaseServiceNamespaceLabel: svc4.Namespace,
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8890),
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       int32(8890),
					TargetPort: intstr.FromInt(8890),
				},
			},
		},

		{
			name:             "multiServiceWithWeb",
			services:         []v1.Service{svc1, svc2},
			backendTarget:    router.BackendTarget{Namespace: svc2.Namespace, Service: svc2.Name},
			exposeAllPorts:   true,
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:                     "test",
				processLabel:                 "web",
				managedServiceLabel:          "true",
				externalServiceLabel:         "true",
				appPoolLabel:                 "",
				appBaseServiceNamespaceLabel: "default",
				appBaseServiceNameLabel:      "test-web",
				"custom1":                    "value1",
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8890),
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       int32(8890),
					TargetPort: intstr.FromInt(8890),
				},
			},
		},

		{
			name:             "multiServiceWithConflictingWeb",
			services:         []v1.Service{svc2, svc5},
			backendTarget:    router.BackendTarget{Namespace: svc2.Namespace, Service: svc2.Name},
			exposeAllPorts:   true,
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:                     "test",
				processLabel:                 "web",
				managedServiceLabel:          "true",
				externalServiceLabel:         "true",
				appPoolLabel:                 "",
				appBaseServiceNamespaceLabel: "default",
				appBaseServiceNameLabel:      "test-web",
				"custom1":                    "value1",
			},
			expectedPorts: []v1.ServicePort{
				{
					Name:       "port-80",
					Protocol:   v1.ProtocolTCP,
					Port:       int32(80),
					TargetPort: intstr.FromInt(8890),
				},
				{
					Protocol:   v1.ProtocolTCP,
					Port:       int32(8890),
					TargetPort: intstr.FromInt(8890),
				},
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := createFakeLBService()

			for i := range tc.services {
				_, err := svc.Client.CoreV1().Services(svc.Namespace).Create(ctx, &tc.services[i], metav1.CreateOptions{})
				require.NoError(t, err)
			}

			err := svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
				Opts: router.Opts{
					AdditionalOpts: map[string]string{
						exposeAllPortsOpt: strconv.FormatBool(tc.exposeAllPorts),
					},
				},
				Prefixes: []router.BackendPrefix{
					{
						Target: tc.backendTarget,
					},
				},
			})

			if tc.expectedErr != nil {
				assert.Equal(t, tc.expectedErr, err)
				return
			}

			require.NoError(t, err)
			setIP(t, svc, "test")
			service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedSelector, service.Spec.Selector)
			assert.Equal(t, tc.expectedPorts, service.Spec.Ports)
			assert.Equal(t, tc.expectedLabels, service.Labels)
		})
	}
}

func TestLBUpdatePortDiffAndPreserveNodePort(t *testing.T) {
	svc := createFakeLBService()
	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				exposeAllPortsOpt: "true",
			},
		},
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
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
	require.NoError(t, err)
	service.Spec.Ports = []v1.ServicePort{
		{
			Name:       "tcp-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(22),
			TargetPort: intstr.FromInt(22),
			NodePort:   31999,
		},
		{
			Name:       "port-80",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(80),
			TargetPort: intstr.FromInt(8888),
			NodePort:   31900,
		},
		{
			Name:       "http-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(8080),
			TargetPort: intstr.FromInt(8080),
			NodePort:   31901,
		},
		{
			Name:       "to-be-removed",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(8081),
			TargetPort: intstr.FromInt(8081),
			NodePort:   31902,
		},
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(ctx, service, metav1.UpdateOptions{})
	require.NoError(t, err)
	webSvc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-web",
			Namespace: "default",
			Labels:    map[string]string{appLabel: "test", processLabel: "web"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-web"},
			Ports: []v1.ServicePort{
				{Name: "http-default-1", Protocol: "TCP", Port: int32(8080), TargetPort: intstr.FromInt(8080), NodePort: 12000},
				{Name: "tcp-default-1", Protocol: "TCP", Port: int32(22), TargetPort: intstr.FromInt(22), NodePort: 12001},
			},
		},
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(ctx, &webSvc, metav1.UpdateOptions{})
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				exposeAllPortsOpt: "true",
			},
		},
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
	service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []v1.ServicePort{
		{
			Name:       "http-default-1-extra",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(80),
			TargetPort: intstr.FromInt(8080),
			NodePort:   31900,
		},
		{
			Name:       "http-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(8080),
			TargetPort: intstr.FromInt(8080),
			NodePort:   31901,
		},
		{
			Name:       "tcp-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(22),
			TargetPort: intstr.FromInt(22),
			NodePort:   31999,
		},
	}, service.Spec.Ports)

	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				exposeAllPortsOpt: "true",
			},
		},
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

	service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []v1.ServicePort{
		{
			Name:       "http-default-1-extra",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(80),
			TargetPort: intstr.FromInt(8080),
			NodePort:   31900,
		},
		{
			Name:       "http-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(8080),
			TargetPort: intstr.FromInt(8080),
			NodePort:   31901,
		},
		{
			Name:       "tcp-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(22),
			TargetPort: intstr.FromInt(22),
			NodePort:   31999,
		},
	}, service.Spec.Ports)

}

func TestLBUpdateNoChangeInFrozenService(t *testing.T) {
	svc := createFakeLBService()
	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				exposeAllPortsOpt: "true",
			},
		},
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
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
	require.NoError(t, err)
	service.Labels = map[string]string{
		routerFreezeLabel: "true",
	}
	service.Spec.Ports = []v1.ServicePort{
		{
			Name:       "tcp-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(1234),
			TargetPort: intstr.FromInt(22),
			NodePort:   31999,
		},
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(ctx, service, metav1.UpdateOptions{})
	require.NoError(t, err)
	webSvc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-web",
			Namespace: "default",
			Labels:    map[string]string{appLabel: "test", processLabel: "web"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-web"},
			Ports: []v1.ServicePort{
				{Name: "tcp-default-1", Protocol: "TCP", Port: int32(22), TargetPort: intstr.FromInt(22), NodePort: 12001},
			},
		},
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(ctx, &webSvc, metav1.UpdateOptions{})
	require.NoError(t, err)
	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				exposeAllPortsOpt: "true",
			},
		},
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
	service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp("test")), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []v1.ServicePort{
		{
			Name:       "tcp-default-1",
			Protocol:   v1.ProtocolTCP,
			Port:       int32(1234),
			TargetPort: intstr.FromInt(22),
			NodePort:   31999,
		},
	}, service.Spec.Ports)
}

func TestGetStatus(t *testing.T) {
	svc := createFakeLBService()

	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
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

	s, err := svc.getLBService(ctx, idForApp("test"))
	require.NoError(t, err)

	_, err = svc.BaseService.Client.CoreV1().Events("default").Create(ctx, &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-1234",
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		InvolvedObject: v1.ObjectReference{
			Name: s.Name,
			UID:  s.UID,
			Kind: "Service",
		},
		Type:    "Warning",
		Reason:  "Unknown reason",
		Message: "Failed to ensure loadbalancer",
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	status, detail, err := svc.GetStatus(ctx, idForApp("test"))
	require.NoError(t, err)

	assert.Equal(t, status, router.BackendStatusNotReady)
	assert.Contains(t, detail, "Warning - Failed to ensure loadbalancer")

	s.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			Hostname: "testing",
			IP:       "66.66.66.66",
		},
	}

	_, err = svc.BaseService.Client.CoreV1().Services("default").UpdateStatus(ctx, s, metav1.UpdateOptions{})
	require.NoError(t, err)

	status, detail, err = svc.GetStatus(ctx, idForApp("test"))
	require.NoError(t, err)

	assert.Equal(t, status, router.BackendStatusReady)
	assert.Contains(t, detail, "")

	s.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			Hostname: "mylb.elb.ZONE.amazonaws.com",
		},
	}

	_, err = svc.BaseService.Client.CoreV1().Services("default").UpdateStatus(ctx, s, metav1.UpdateOptions{})
	require.NoError(t, err)

	status, detail, err = svc.GetStatus(ctx, idForApp("test"))
	require.NoError(t, err)

	assert.Equal(t, status, router.BackendStatusReady)
	assert.Contains(t, detail, "")
}

func TestGetAddresses(t *testing.T) {
	svc := createFakeLBService()

	err := createAppWebService(svc.Client, svc.Namespace, "test")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("test"), router.EnsureBackendOpts{
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

	s, err := svc.getLBService(ctx, idForApp("test"))
	require.NoError(t, err)

	addresses, err := svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{""}, addresses)

	s.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			Hostname: "testing.io",
			IP:       "66.66.66.66",
		},
	}
	_, err = svc.BaseService.Client.CoreV1().Services("default").UpdateStatus(ctx, s, metav1.UpdateOptions{})
	require.NoError(t, err)

	addresses, err = svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"testing.io"}, addresses)

	s.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			Hostname: "mylb.elb.ZONE.amazonaws.com",
		},
	}
	_, err = svc.BaseService.Client.CoreV1().Services("default").UpdateStatus(ctx, s, metav1.UpdateOptions{})
	require.NoError(t, err)

	addresses, err = svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)

	assert.Equal(t, []string{"mylb.elb.ZONE.amazonaws.com"}, addresses)

	s.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{
		{
			IP: "66.66.66.66",
		},
	}
	s.Annotations[externalDNSHostnameLabel] = "myapp.zone.io,myapp.com"
	_, err = svc.BaseService.Client.CoreV1().Services("default").UpdateStatus(ctx, s, metav1.UpdateOptions{})
	require.NoError(t, err)

	addresses, err = svc.GetAddresses(ctx, idForApp("test"))
	require.NoError(t, err)

	assert.Equal(t, []string{"myapp.zone.io", "myapp.com"}, addresses)
}

func defaultService(app, namespace string, labels, annotations, selector map[string]string) *v1.Service {
	if selector == nil {
		selector = map[string]string{
			"tsuru.io/app-name":    app,
			"tsuru.io/app-process": "web",
		}
	}
	svc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app + "-router-lb",
			Namespace: namespace,
			Labels: map[string]string{
				appLabel:                     app,
				managedServiceLabel:          "true",
				externalServiceLabel:         "true",
				appPoolLabel:                 "",
				appBaseServiceNameLabel:      app + "-web",
				appBaseServiceNamespaceLabel: namespace,
			},
			Annotations: annotations,
		},
		Spec: v1.ServiceSpec{
			Selector: selector,
			Type:     v1.ServiceTypeLoadBalancer,
			Ports: []v1.ServicePort{
				{
					Name:       fmt.Sprintf("port-%d", defaultLBPort),
					Protocol:   "TCP",
					Port:       int32(defaultLBPort),
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
		Status: v1.ServiceStatus{
			LoadBalancer: v1.LoadBalancerStatus{
				Ingress: []v1.LoadBalancerIngress{
					{IP: "127.0.0.1"},
				},
			},
		},
	}
	for k, v := range labels {
		svc.ObjectMeta.Labels[k] = v
	}
	return &svc
}

func setIP(t *testing.T, svc LBService, appName string) {
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(ctx, svc.serviceName(idForApp(appName)), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Expected err to be nil. Got %v", err)
	}
	service.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{IP: "127.0.0.1"}}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(ctx, service, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Expected err to be nil. Got %v", err)
	}
}
