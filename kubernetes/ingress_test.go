// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"reflect"
	"sort"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

func createFakeService() IngressService {
	return IngressService{
		Namespace: "default",
		Client:    fake.NewSimpleClientset(),
	}
}

func TestCreate(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(ingressList.Items))
	}
	expectedIngress := defaultIngress("test")
	if !reflect.DeepEqual(ingressList.Items[0], expectedIngress) {
		t.Errorf("Expected %v. Got %v", expectedIngress, ingressList.Items[0])
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
				_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&tc.services[i])
				if err != nil {
					t.Errorf("Expected err to be nil. Got %v.", err)
				}
			}

			err = svc.Update("test")
			if err != tc.expectedErr {
				t.Errorf("Expected err to be %v. Got %v.", tc.expectedErr, err)
			}
			ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
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

func TestSwap(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Create("test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	err = svc.Swap("test-blue", "test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	sort.Slice(ingressList.Items, func(i, j int) bool {
		return ingressList.Items[i].Name < ingressList.Items[j].Name
	})
	blueIng := defaultIngress("test-blue")
	blueIng.Labels[swapLabel] = "test-green"
	blueIng.Spec.Backend.ServiceName = "test-green"
	greenIng := defaultIngress("test-green")
	greenIng.Labels[swapLabel] = "test-blue"
	greenIng.Spec.Backend.ServiceName = "test-blue"

	if !reflect.DeepEqual(ingressList.Items, []v1beta1.Ingress{blueIng, greenIng}) {
		t.Errorf("Expected %v. Got %v", []v1beta1.Ingress{blueIng, greenIng}, ingressList.Items)
	}

	err = svc.Swap("test-blue", "test-green")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	ingressList, err = svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	sort.Slice(ingressList.Items, func(i, j int) bool {
		return ingressList.Items[i].Name < ingressList.Items[j].Name
	})
	delete(blueIng.Labels, swapLabel)
	blueIng.Spec.Backend.ServiceName = "test-blue"
	delete(greenIng.Labels, swapLabel)
	greenIng.Spec.Backend.ServiceName = "test-green"

	if !reflect.DeepEqual(ingressList.Items, []v1beta1.Ingress{blueIng, greenIng}) {
		t.Errorf("Expected %v. Got %v", []v1beta1.Ingress{blueIng, greenIng}, ingressList.Items)
	}
}

func TestAddresses(t *testing.T) {
	svc1 := apiv1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      "test",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "test", appPoolLabel: "pool"},
	},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899), NodePort: 9090}},
		},
	}
	node := apiv1.Node{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{poolLabel: "pool"},
	},
		Status: apiv1.NodeStatus{Addresses: []apiv1.NodeAddress{{Type: apiv1.NodeInternalIP, Address: "192.168.10.1"}}},
	}
	svc := createFakeService()
	err := svc.Create("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&svc1)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	_, err = svc.Client.CoreV1().Nodes().Create(&node)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	expected := []string{"http://192.168.10.1:9090"}
	addresses, err := svc.Addresses("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if !reflect.DeepEqual(addresses, expected) {
		t.Errorf("Expected %v. Got %v.", expected, addresses)
	}
}

func TestRemove(t *testing.T) {
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
			svc := createFakeService()
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
			ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			if len(ingressList.Items) != tc.expectedCount {
				t.Errorf("Expected %d items. Got %d.", tc.expectedCount, len(ingressList.Items))
			}
		})
	}
}

func defaultIngress(name string) v1beta1.Ingress {
	return v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-ingress",
			Namespace: "default",
			Labels:    map[string]string{appLabel: name},
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: name,
				ServicePort: intstr.FromInt(8888),
			},
		},
	}
}
