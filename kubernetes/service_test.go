// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	apiv1 "k8s.io/client-go/pkg/api/v1"
)

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
