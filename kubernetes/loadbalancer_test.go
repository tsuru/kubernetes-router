// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

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

func TestLBCreate(t *testing.T) {
	svc := createFakeLBService()
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"annotation": "annval"}
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}
	err := svc.Create(idForApp("test"), router.Opts{Pool: "mypool", AdditionalOpts: map[string]string{"my-opt": "value"}})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	setIP(t, svc, "test")
	serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(serviceList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(serviceList.Items))
	}
	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["my-opt-as-label"] = "value"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"annotation":           "annval",
		"router.tsuru.io/opts": `{"Pool":"mypool","AdditionalOpts":{"my-opt":"value"}}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	assert.Equal(t, expectedService, serviceList.Items[0])
}

func TestLBCreateCustomAnnotation(t *testing.T) {
	svc := createFakeLBService()
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.PoolLabels = map[string]map[string]string{"mypool": {"pool-env": "dev"}, "otherpool": {"pool-env": "prod"}}
	err := svc.Create(idForApp("test"), router.Opts{
		Pool: "mypool",
		AdditionalOpts: map[string]string{
			"my-opt":    "value",
			"other-opt": "other-value",
			"ann1-":     "",
		}})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	setIP(t, svc, "test")
	serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(serviceList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(serviceList.Items))
	}
	svc.Labels[appPoolLabel] = "mypool"
	svc.Labels["my-opt-as-label"] = "value"
	svc.Labels["pool-env"] = "dev"
	expectedAnnotations := map[string]string{
		"ann2":                 "val2",
		"other-opt":            "other-value",
		"router.tsuru.io/opts": `{"Pool":"mypool","AdditionalOpts":{"ann1-":"","my-opt":"value","other-opt":"other-value"}}`,
	}
	expectedService := defaultService("test", "default", svc.Labels, expectedAnnotations, nil)
	assert.Equal(t, expectedService, serviceList.Items[0])
}

func TestLBCreateDefaultPort(t *testing.T) {
	svc := createFakeLBService()
	if err := createCRD(svc.BaseService, "myapp", "custom-namespace", nil); err != nil {
		t.Errorf("failed to create CRD for test: %v", err)
	}
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
	err := svc.Create(idForApp("myapp"), router.Opts{Pool: "mypool", AdditionalOpts: map[string]string{"my-opt": "value"}})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
}

func TestLBSupportedOptions(t *testing.T) {
	svc := createFakeLBService()
	svc.OptsAsLabels["my-opt"] = "my-opt-as-label"
	svc.OptsAsLabels["my-opt2"] = "my-opt-as-label2"
	svc.OptsAsLabelsDocs["my-opt2"] = "User friendly option description."
	options := svc.SupportedOptions()
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

func TestLBCreateAppNamespace(t *testing.T) {
	svc := createFakeLBService()
	if err := createCRD(svc.BaseService, "app", "custom-namespace", nil); err != nil {
		t.Errorf("failed to create CRD for test: %v", err)
	}
	if err := svc.Create(idForApp("app"), router.Opts{}); err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	serviceList, err := svc.Client.CoreV1().Services("custom-namespace").List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
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
		{"success", "test", nil, 2},
		{"failSwapped", "blue", ErrAppSwapped{App: "blue", DstApp: "green"}, 3},
		{"ignoresNotFound", "notfound", nil, 3},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			svc := createFakeLBService()
			err := svc.Create(idForApp("test"), router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			setIP(t, svc, "test")
			err = svc.Create(idForApp("blue"), router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			setIP(t, svc, "blue")
			err = svc.Create(idForApp("green"), router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			setIP(t, svc, "green")
			err = svc.Swap(idForApp("blue"), idForApp("green"))
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Remove(idForApp(tc.remove))
			if err != tc.expectedErr {
				t.Errorf("Expected err to be %v. Got %v.", tc.expectedErr, err)
			}
			serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			if len(serviceList.Items) != tc.expectedCount {
				t.Errorf("Expected %d items. Got %d.", tc.expectedCount, len(serviceList.Items))
			}
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
		extraData        router.RoutesRequestExtraData
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
			expectedSelector: map[string]string{"name": "test-single"},
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
			expectedSelector: map[string]string{"name": "test-single"},
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
					TargetPort: intstr.FromInt(8899),
				},
			},
		},
		{
			name:             "multiServiceWithWeb",
			services:         []v1.Service{svc1, svc2},
			exposeAllPorts:   true,
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				processLabel:         "web",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
				"custom1":            "value1",
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
			expectedErr: ErrNoService{App: "test", Process: "web"},
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
			expectedSelector: map[string]string{"name": "test-web"},
			expectedLabels: map[string]string{
				appLabel:             "test",
				processLabel:         "web",
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
				"custom1":            "value1",
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
			extraData:        router.RoutesRequestExtraData{Namespace: svc2.Namespace, Service: svc2.Name},
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
			extraData:        router.RoutesRequestExtraData{Namespace: svc2.Namespace, Service: svc2.Name},
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
			err := svc.Create(idForApp("test"), router.Opts{AdditionalOpts: map[string]string{
				exposeAllPortsOpt: strconv.FormatBool(tc.exposeAllPorts),
			}})
			assert.NoError(t, err)

			setIP(t, svc, "test")
			for i := range tc.services {
				_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&tc.services[i])
				assert.NoError(t, err)
			}

			err = svc.Update(idForApp("test"), tc.extraData)
			if tc.expectedErr != nil {
				assert.Equal(t, err, tc.expectedErr)
			} else {
				assert.NoError(t, err)
			}

			service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test")), metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedSelector, service.Spec.Selector)
			assert.Equal(t, tc.expectedPorts, service.Spec.Ports)
			assert.Equal(t, tc.expectedLabels, service.Labels)

			err = svc.Create(idForApp("test"), router.Opts{AdditionalOpts: map[string]string{
				exposeAllPortsOpt: strconv.FormatBool(tc.exposeAllPorts),
			}})
			assert.NoError(t, err)

			service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test")), metav1.GetOptions{})
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedSelector, service.Spec.Selector)
			assert.Equal(t, tc.expectedPorts, service.Spec.Ports)
			assert.Equal(t, tc.expectedLabels, service.Labels)
		})
	}
}

func TestLBUpdatePortDiffAndPreserveNodePort(t *testing.T) {
	svc := createFakeLBService()
	err := svc.Create(idForApp("test"), router.Opts{AdditionalOpts: map[string]string{
		exposeAllPortsOpt: "true",
	}})
	require.NoError(t, err)
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test")), metav1.GetOptions{})
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
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(service)
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
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&webSvc)
	require.NoError(t, err)
	err = svc.Update(idForApp("test"), router.RoutesRequestExtraData{})
	require.NoError(t, err)
	service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test")), metav1.GetOptions{})
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

	err = svc.Create(idForApp("test"), router.Opts{AdditionalOpts: map[string]string{
		exposeAllPortsOpt: "true",
	}})
	require.NoError(t, err)

	service, err = svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test")), metav1.GetOptions{})
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

func TestLBUpdateSwapped(t *testing.T) {
	svc := createFakeLBService()
	for _, n := range []string{"blue", "green"} {
		err := svc.Create(idForApp("test-"+n), router.Opts{})
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
		setIP(t, svc, "test-"+n)
		err = createWebService(n, "default", svc.Client)
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
		err = svc.Update(idForApp("test-"+n), router.RoutesRequestExtraData{})
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
	}
	err := svc.Swap(idForApp("test-blue"), idForApp("test-green"))
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Update(idForApp("test-blue"), router.RoutesRequestExtraData{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp("test-blue")), metav1.GetOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	expectedSelector := map[string]string{"app": "green"}
	if !reflect.DeepEqual(service.Spec.Selector, expectedSelector) {
		t.Errorf("Expected %v. Got %v", expectedSelector, service.Spec.Selector)
	}
}

func TestLBSwap(t *testing.T) {
	svc := createFakeLBService()

	for _, n := range []string{"blue", "green"} {
		err := svc.Create(idForApp("test-"+n), router.Opts{})
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
		setIP(t, svc, "test-"+n)
		err = createWebService(n, "default", svc.Client)
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
		err = svc.Update(idForApp("test-"+n), router.RoutesRequestExtraData{})
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
	}

	blueSvc := defaultService("test-blue", "default", map[string]string{swapLabel: "test-green"}, map[string]string{"router.tsuru.io/opts": "{}"}, map[string]string{"app": "green"})
	greenSvc := defaultService("test-green", "default", map[string]string{swapLabel: "test-blue"}, map[string]string{"router.tsuru.io/opts": "{}"}, map[string]string{"app": "blue"})
	isSwapped := true
	i := 1
	for i <= 2 {
		err := svc.Swap(idForApp("test-blue"), idForApp("test-green"))
		if err != nil {
			t.Errorf("Iteration %d: Expected err to be nil. Got %v.", i, err)
		}
		serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=true", managedServiceLabel),
		})
		if err != nil {
			t.Errorf("Iteration %d: Expected err to be nil. Got %v.", i, err)
		}
		if !reflect.DeepEqual(serviceList.Items, []v1.Service{blueSvc, greenSvc}) {
			t.Errorf("Iteration %d: Expected %#v. \nGot %#v", i, []v1.Service{blueSvc, greenSvc}, serviceList.Items)
		}
		if _, swapped := svc.BaseService.isSwapped(blueSvc.ObjectMeta); swapped != isSwapped {
			t.Errorf("Iteration %d: Expected isSwapped to be %v. Got %v", i, isSwapped, swapped)
		}

		blueSvc = defaultService("test-blue", "default", map[string]string{swapLabel: ""}, map[string]string{"router.tsuru.io/opts": "{}"}, map[string]string{"app": "blue"})
		greenSvc = defaultService("test-green", "default", map[string]string{swapLabel: ""}, map[string]string{"router.tsuru.io/opts": "{}"}, map[string]string{"app": "green"})

		isSwapped = !isSwapped
		i++
	}
}

func TestLBUpdateSwapWithouIPFails(t *testing.T) {
	svc := createFakeLBService()
	err := createWebService("myapp1", "default", svc.Client)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = createWebService("myapp2", "default", svc.Client)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Create(idForApp("test-myapp1"), router.Opts{Pool: "mypool"})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Update(idForApp("test-myapp1"), router.RoutesRequestExtraData{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Create(idForApp("test-myapp2"), router.Opts{Pool: "mypool"})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Swap(idForApp("test-myapp1"), idForApp("test-myapp2"))
	if err != ErrLoadBalancerNotReady {
		t.Fatalf("Expected err to be %v. Got %v.", ErrLoadBalancerNotReady, err)
	}
	setIP(t, svc, "test-myapp1")
	err = svc.Swap(idForApp("test-myapp1"), idForApp("test-myapp2"))
	if err != ErrLoadBalancerNotReady {
		t.Fatalf("Expected err to be %v. Got %v.", ErrLoadBalancerNotReady, err)
	}
	setIP(t, svc, "test-myapp2")
	err = svc.Swap(idForApp("test-myapp1"), idForApp("test-myapp2"))
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
}

func createWebService(app, namespace string, client kubernetes.Interface) error {
	webService := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app + "-web",
			Namespace: namespace,
			Labels:    map[string]string{appLabel: "test-" + app},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"app": app},
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       int32(defaultLBPort),
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}
	_, err := client.CoreV1().Services(namespace).Create(webService)
	return err
}

func defaultService(app, namespace string, labels, annotations, selector map[string]string) v1.Service {
	svc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app + "-router-lb",
			Namespace: namespace,
			Labels: map[string]string{
				appLabel:             app,
				managedServiceLabel:  "true",
				externalServiceLabel: "true",
				appPoolLabel:         "",
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
	return svc
}

func setIP(t *testing.T, svc LBService, appName string) {
	service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(svc.serviceName(idForApp(appName)), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Expected err to be nil. Got %v", err)
	}
	service.Status.LoadBalancer.Ingress = []v1.LoadBalancerIngress{{IP: "127.0.0.1"}}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Update(service)
	if err != nil {
		t.Fatalf("Expected err to be nil. Got %v", err)
	}
}
