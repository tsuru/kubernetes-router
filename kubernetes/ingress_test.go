// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

func createFakeService() IngressService {
	fakeRest := fake.NewSimpleClientset()
	svc := IngressService{Namespace: "default"}
	svc.client = fakeRest
	return svc
}

func TestCreate(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err := svc.client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(ingressList.Items))
	}
	expectedIngress := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ingress",
			Namespace: "default",
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: "test",
				ServicePort: intstr.FromInt(8888),
			},
		},
	}
	if !reflect.DeepEqual(ingressList.Items[0], expectedIngress) {
		t.Errorf("Expected %v. Got %v", expectedIngress.Spec, ingressList.Items[0].Spec)
	}
}

func TestUpdate(t *testing.T) {
	svc1 := apiv1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      "test-single",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "test"},
	},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899)}},
		},
	}
	svc2 := apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-web",
			Namespace: "default",
			Labels:    map[string]string{appLabel: "test", processLabel: "web"},
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{Protocol: "TCP", Port: int32(8890), TargetPort: intstr.FromInt(8890)}},
		},
	}
	svc3 := svc2
	svc3.ObjectMeta.Labels = svc1.ObjectMeta.Labels
	defaultBackend := v1beta1.IngressBackend{ServiceName: "test", ServicePort: intstr.FromInt(8888)}
	tt := []struct {
		name            string
		services        []apiv1.Service
		expectedErr     error
		expectedBackend v1beta1.IngressBackend
	}{
		{name: "noServices", services: []apiv1.Service{}, expectedErr: ErrNoService{App: "test"}, expectedBackend: defaultBackend},
		{name: "singleService", services: []apiv1.Service{svc1}, expectedBackend: v1beta1.IngressBackend{ServiceName: "test-single", ServicePort: intstr.FromInt(8899)}},
		{name: "multiServiceWithWeb", services: []apiv1.Service{svc1, svc2}, expectedBackend: v1beta1.IngressBackend{ServiceName: "test-web", ServicePort: intstr.FromInt(8890)}},
		{name: "multiServiceWithoutWeb", services: []apiv1.Service{svc1, svc3}, expectedErr: ErrNoService{App: "test", Process: "web"}, expectedBackend: defaultBackend},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := createFakeService()
			err := svc.Create("test")
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			for i := range tc.services {
				_, err := svc.client.CoreV1().Services(svc.Namespace).Create(&tc.services[i])
				if err != nil {
					t.Errorf("Expected err to be nil. Got %v.", err)
				}
			}

			err = svc.Update("test")
			if err != tc.expectedErr {
				t.Errorf("Expected err to be %v. Got %v.", tc.expectedErr, err)
			}
			ingressList, err := svc.client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			if len(ingressList.Items) != 1 {
				t.Errorf("Expected 1 item. Got %d.", len(ingressList.Items))
			}
			if !reflect.DeepEqual(ingressList.Items[0].Spec.Backend, &tc.expectedBackend) {
				t.Errorf("Expected %v. Got %v", tc.expectedBackend, ingressList.Items[0].Spec.Backend)
			}
		})
	}
}

func TestRemoveIgnoresNotFound(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err := svc.client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(ingressList.Items))
	}
	err = svc.Remove("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err = svc.client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 0 {
		t.Errorf("Expected 0 items. Got %d.", len(ingressList.Items))
	}
}

func TestRemove(t *testing.T) {
	svc := createFakeService()
	err := svc.Remove("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err := svc.client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 0 {
		t.Errorf("Expected 0 items. Got %d.", len(ingressList.Items))
	}
}
