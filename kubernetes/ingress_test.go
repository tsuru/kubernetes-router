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
