// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"reflect"
	"testing"

	"github.com/tsuru/kubernetes-router/router"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/pkg/api/v1"
)

func TestAddresses(t *testing.T) {
	svc1 := v1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      "test",
		Namespace: "default",
		Labels:    map[string]string{appLabel: "test", appPoolLabel: "pool"},
	},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{{Protocol: "TCP", Port: int32(8899), TargetPort: intstr.FromInt(8899), NodePort: 9090}},
		},
	}
	node := v1.Node{ObjectMeta: metav1.ObjectMeta{
		Labels: map[string]string{poolLabel: "pool"},
	},
		Status: v1.NodeStatus{Addresses: []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: "192.168.10.1"}}},
	}
	svc := createFakeService()
	err := svc.Create("test", router.Opts{})
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

func TestGetWebService(t *testing.T) {
	headlessSvc := v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-headless",
			Namespace: "default",
			Labels:    map[string]string{appLabel: "test", headlessServiceLabel: "true"},
		},
		Spec: v1.ServiceSpec{
			Selector: map[string]string{"name": "test"},
		},
	}
	svc := BaseService{Namespace: "default", Client: fake.NewSimpleClientset()}
	_, err := svc.Client.CoreV1().Services(svc.Namespace).Create(&headlessSvc)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	webService, err := svc.getWebService("test")
	expectedErr := ErrNoService{App: "test"}
	if err != expectedErr {
		t.Errorf("Expected err to be %v. Got %v. Got service: %v", expectedErr, err, webService)
	}
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
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&svc1)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
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
	_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&svc2)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	webService, err = svc.getWebService("test")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if webService.Name != "test-web" {
		t.Errorf("Expected service to be %v. Got %v.", svc2, webService)
	}
}
