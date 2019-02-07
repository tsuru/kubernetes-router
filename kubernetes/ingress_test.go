// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"reflect"
	"sort"
	"testing"

	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	apiv1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func createFakeService() IngressService {
	return IngressService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           fake.NewSimpleClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
	}
}

func TestSecretName(t *testing.T) {
	appName := "tsuru-dashboard"
	certName := "biiigerdomain.cloud.evenbiiiiiiiiigerrrrr.com"
	sName := secretName(appName, certName)
	if sName != "kr-742c4ad94b87ba0d5895d073540d9629d86b97da" {
		t.Errorf("SecretName Got %v.", sName)
	}
	if len(sName) > 63 {
		t.Errorf("SecretName too big, something went wrong.")
	}
	appName = "tsuru-dashboard"
	certName = "domain.com"
	sName = secretName(appName, certName)
	if sName != "kr-tsuru-dashboard-domain.com" {
		t.Errorf("SecretName Got %v.", sName)
	}
	if len(sName) > 63 {
		t.Errorf("SecretName too big, something went wrong.")
	}
}

func TestCreate(t *testing.T) {
	svc := createFakeService()
	svc.Labels = map[string]string{"controller": "my-controller", "XPTO": "true"}
	svc.Annotations = map[string]string{"ann1": "val1", "ann2": "val2"}
	err := svc.Create("test", router.Opts{})
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
	expectedIngress := defaultIngress("test", "default")
	expectedIngress.Labels["controller"] = "my-controller"
	expectedIngress.Labels["XPTO"] = "true"
	expectedIngress.Annotations["ann1"] = "val1"
	expectedIngress.Annotations["ann2"] = "val2"

	if !reflect.DeepEqual(ingressList.Items[0], expectedIngress) {
		t.Errorf("Expected %v. Got %v", expectedIngress, ingressList.Items[0])
	}
}

func TestCreateIngressAppNamespace(t *testing.T) {
	svc := createFakeService()
	if err := createCRD(svc.BaseService, "app", "custom-namespace", nil); err != nil {
		t.Errorf("failed to create CRD for test: %v", err)
	}
	if err := svc.Create("app", router.Opts{}); err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses("custom-namespace").List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if len(ingressList.Items) != 1 {
		t.Errorf("Expected 1 item. Got %d.", len(ingressList.Items))
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
			err := svc.Create("test", router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			for i := range tc.services {
				_, err = svc.Client.CoreV1().Services(svc.Namespace).Create(&tc.services[i])
				if err != nil {
					t.Errorf("Expected err to be nil. Got %v.", err)
				}
			}

			err = svc.Update("test", router.Opts{})
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
			if !reflect.DeepEqual(ingressList.Items[0].Spec.Rules[0].HTTP.Paths[0].Backend, tc.expectedBackend) {
				t.Errorf("Expected %v. Got %v", tc.expectedBackend, ingressList.Items[0].Spec.Rules[0].HTTP.Paths[0].Backend)
			}
		})
	}
}

func TestSwap(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.Create("test-green", router.Opts{})
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
	blueIng := defaultIngress("test-blue", "default")
	blueIng.Labels[swapLabel] = "test-green"
	blueIng.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = "test-green"
	greenIng := defaultIngress("test-green", "default")
	greenIng.Labels[swapLabel] = "test-blue"
	greenIng.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = "test-blue"

	for _, ing := range ingressList.Items {
		if ing.GetName() == blueIng.GetName() {
			if !reflect.DeepEqual(ing.Spec.Rules[0], blueIng.Spec.Rules[0]) {
				t.Errorf("Expected %v. Got %v", blueIng.Spec.Rules[0], ing.Spec.Rules[0])
			}
		} else if ing.GetName() == greenIng.GetName() {
			if !reflect.DeepEqual(ing.Spec.Rules[0], greenIng.Spec.Rules[0]) {
				t.Errorf("Expected %v. Got %v", greenIng.Spec.Rules[0], ing.Spec.Rules[0])
			}
		}
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
	blueIng.Labels[swapLabel] = ""
	blueIng.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = "test-blue"
	greenIng.Labels[swapLabel] = ""
	greenIng.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = "test-green"

	for _, ing := range ingressList.Items {
		if ing.GetName() == blueIng.GetName() {
			if !reflect.DeepEqual(ing.Spec.Rules[0], blueIng.Spec.Rules[0]) {
				t.Errorf("Expected %v. Got %v", blueIng.Spec.Rules[0], ing.Spec.Rules[0])
			}
		} else if ing.GetName() == greenIng.GetName() {
			if !reflect.DeepEqual(ing.Spec.Rules[0], greenIng.Spec.Rules[0]) {
				t.Errorf("Expected %v. Got %v", greenIng.Spec.Rules[0], ing.Spec.Rules[0])
			}
		}
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
			err := svc.Create("test", router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Create("blue", router.Opts{})
			if err != nil {
				t.Errorf("Expected err to be nil. Got %v.", err)
			}
			err = svc.Create("green", router.Opts{})
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

func TestUnsetCname(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.SetCname("test-blue", "cname1")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.UnsetCname("test-blue", "cname1")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	cnameIng := defaultIngress("test-blue", "default")
	cnameIng.Annotations[annotationWithPrefix("server-alias")] = ""

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	if !reflect.DeepEqual(ingressList, &v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}) {
		t.Errorf("Expected %v. Got %v", v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}, ingressList)
	}
}

func TestSetCname(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.SetCname("test-blue", "cname1")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	cnameIng := defaultIngress("test-blue", "default")
	cnameIng.Annotations[annotationWithPrefix("server-alias")] = "cname1"

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	if !reflect.DeepEqual(ingressList, &v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}) {
		t.Errorf("Expected %v. Got %v", v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}, ingressList)
	}
}

func TestGetCnames(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.SetCname("test-blue", "cname1")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.SetCname("test-blue", "cname2")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	cnameIng := defaultIngress("test-blue", "default")
	cnameIng.Annotations[annotationWithPrefix("server-alias")] = "cname1 cname2"

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	if !reflect.DeepEqual(ingressList, &v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}) {
		t.Errorf("Expected %v. Got %v", v1beta1.IngressList{Items: []v1beta1.Ingress{cnameIng}}, ingressList)
	}

	cnames, err := svc.GetCnames("test-blue")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if !reflect.DeepEqual(cnames, &router.CnamesResp{Cnames: []string{"cname1", "cname2"}}) {
		t.Errorf("Expected %v. Got %v", &router.CnamesResp{Cnames: []string{"cname1", "cname2"}}, cnames)
	}
}

func TestRemoveCertificate(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate("test-blue", "mycert", expectedCert)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	err = svc.RemoveCertificate("test-blue", "mycert")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
}

func TestAddCertificate(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate("test-blue", "mycert", expectedCert)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]v1beta1.IngressTLS{
			{
				Hosts:      []string{"mycert"},
				SecretName: secretName("test-blue", "mycert"),
			},
		}...)

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	if !reflect.DeepEqual(ingressList.Items[0].Spec.TLS, certTest.Spec.TLS) {
		t.Errorf("Expected %v. Got %v", &v1beta1.IngressList{Items: []v1beta1.Ingress{certTest}}, ingressList)
	}
}

func TestGetCertificate(t *testing.T) {
	svc := createFakeService()
	err := svc.Create("test-blue", router.Opts{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	expectedCert := router.CertData{Certificate: "Certz", Key: "keyz"}
	err = svc.AddCertificate("test-blue", "mycert", expectedCert)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	certTest := defaultIngress("test-blue", "default")
	certTest.Spec.TLS = append(certTest.Spec.TLS,
		[]v1beta1.IngressTLS{
			{
				Hosts:      []string{"mycert"},
				SecretName: secretName("test-blue", "mycert"),
			},
		}...)

	ingressList, err := svc.Client.ExtensionsV1beta1().Ingresses(svc.Namespace).List(metav1.ListOptions{})
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}

	if !reflect.DeepEqual(ingressList.Items[0].Spec.TLS, certTest.Spec.TLS) {
		t.Errorf("Expected %v. Got %v", &v1beta1.IngressList{Items: []v1beta1.Ingress{certTest}}, ingressList)
	}

	cert, err := svc.GetCertificate("test-blue", "mycert")
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v.", err)
	}
	if !reflect.DeepEqual(cert, &router.CertData{Certificate: "", Key: ""}) {
		t.Errorf("Expected %v. Got %v", &expectedCert, cert)
	}
}

func defaultIngress(name, namespace string) v1beta1.Ingress {
	return v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ingressName(name),
			Namespace:   namespace,
			Labels:      map[string]string{appLabel: name},
			Annotations: make(map[string]string),
		},
		Spec: v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: name + ".",
					IngressRuleValue: v1beta1.IngressRuleValue{
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath{
								{
									Path: "",
									Backend: v1beta1.IngressBackend{
										ServiceName: name,
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
