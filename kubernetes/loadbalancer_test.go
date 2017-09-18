// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/api/v1"
)

func createFakeLBService() LBService {
	return LBService{
		BaseService: &BaseService{
			Namespace: "default",
			Client:    fake.NewSimpleClientset(),
		},
	}
}

func defaultService(app string) v1.Service {
	return v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(app),
			Namespace: "default",
			Labels:    map[string]string{appLabel: app, managedServiceLabel: "true"},
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeLoadBalancer,
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       int32(defaultServicePort),
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}
}

func TestLBCreate(t *testing.T) {
	svc := createFakeLBService()
	svc.Labels = map[string]string{"label": "labelval"}
	svc.Annotations = map[string]string{"annotation": "annval"}
	err := svc.Create("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(serviceList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(serviceList.Items))
	}
	expectedService := defaultService("test")
	expectedService.Labels["label"] = "labelval"
	expectedService.Annotations = map[string]string{"annotation": "annval"}
	if !reflect.DeepEqual(serviceList.Items[0], expectedService) {
		t.Errorf("Expected %v. Got %v", expectedService, serviceList.Items[0])
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
			err := svc.Create("test")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Create("blue")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Create("green")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Swap("blue", "green")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Remove(tc.remove)
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
		Name:      "test-single",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "test"},
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
			Labels:    map[string]string{appLabel: "test", processLabel: "web"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test-web"},
			Ports:    []v1.ServicePort{{Protocol: "TCP", Port: int32(8890), TargetPort: intstr.FromInt(8890)}},
		},
	}
	svc3 := svc2
	svc3.ObjectMeta.Labels = svc1.ObjectMeta.Labels
	tt := []struct {
		name             string
		services         []v1.Service
		expectedErr      error
		expectedSelector map[string]string
	}{
		{name: "noServices", services: []v1.Service{}, expectedErr: ErrNoService{App: "test"}},
		{name: "singleService", services: []v1.Service{svc1}, expectedSelector: map[string]string{"name": "test-single"}},
		{name: "multiServiceWithWeb", services: []v1.Service{svc1, svc2}, expectedSelector: map[string]string{"name": "test-web"}},
		{name: "multiServiceWithoutWeb", services: []v1.Service{svc1, svc3}, expectedErr: ErrNoService{App: "test", Process: "web"}},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := createFakeLBService()
			err := svc.Create("test")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			for i := range tc.services {
				_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&tc.services[i])
				if err != nil {
					t.Errorf("Expected err to be nil. Got %v.", err)
				}
			}

			err = svc.Update("test")
			if err != tc.expectedErr {
				t.Errorf("Expected err to be %v. Got %v.", tc.expectedErr, err)
			}
			service, err := svc.Client.CoreV1().Services(svc.Namespace).Get(serviceName("test"), metav1.GetOptions{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			if !reflect.DeepEqual(service.Spec.Selector, tc.expectedSelector) {
				t.Errorf("Expected %v. Got %v", tc.expectedSelector, service.Spec.Selector)
			}
		})
	}
}

func TestLBSwap(t *testing.T) {
	svc := createFakeLBService()
	err := svc.Create("test-blue")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Create("test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	for _, n := range []string{"blue", "green"} {
		webService := &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      n + "-web",
				Namespace: "default",
				Labels:    map[string]string{appLabel: "test-" + n},
			},
			Spec: v1.ServiceSpec{
				Selector: map[string]string{"app": n},
				Ports: []v1.ServicePort{
					{
						Protocol:   "TCP",
						Port:       int32(defaultServicePort),
						TargetPort: intstr.FromInt(defaultServicePort),
					},
				},
			},
		}
		_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(webService)
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
		err = svc.Update("test-" + n)
		if err != nil {
			t.Errorf("Expected err to be nil. Got %v.", err)
		}
	}

	err = svc.Swap("test-blue", "test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	serviceList, err := svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", managedServiceLabel),
	})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	sort.Slice(serviceList.Items, func(i, j int) bool {
		return serviceList.Items[i].Name < serviceList.Items[j].Name
	})
	blueSvc := defaultService("test-blue")
	blueSvc.Labels[swapLabel] = "test-green"
	blueSvc.Spec.Selector = map[string]string{"app": "green"}
	greenSvc := defaultService("test-green")
	greenSvc.Labels[swapLabel] = "test-blue"
	greenSvc.Spec.Selector = map[string]string{"app": "blue"}

	if !reflect.DeepEqual(serviceList.Items, []v1.Service{blueSvc, greenSvc}) {
		t.Errorf("Expected %+v. \nGot %+v", []v1.Service{blueSvc, greenSvc}, serviceList.Items)
	}

	err = svc.Swap("test-blue", "test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	serviceList, err = svc.Client.CoreV1().Services(svc.Namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", managedServiceLabel),
	})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	sort.Slice(serviceList.Items, func(i, j int) bool {
		return serviceList.Items[i].Name < serviceList.Items[j].Name
	})
	delete(blueSvc.Labels, swapLabel)
	blueSvc.Spec.Selector = map[string]string{"app": "blue"}
	delete(greenSvc.Labels, swapLabel)
	greenSvc.Spec.Selector = map[string]string{"app": "green"}

	if !reflect.DeepEqual(serviceList.Items, []v1.Service{blueSvc, greenSvc}) {
		t.Errorf("Expected %v. Got %v", []v1.Service{blueSvc, greenSvc}, serviceList.Items)
	}
}
