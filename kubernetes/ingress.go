// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	typedV1Beta1 "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
	"k8s.io/client-go/rest"
)

const (
	servicePort = 8888

	ingressNameFmt = "%s-ingress"
)

var deletePropagation = metav1.DeletePropagationForeground

type IngressService struct {
	Namespace string
	client    kubernetes.Interface
}

func (k *IngressService) Create(name string) error {
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	ingress := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf(ingressNameFmt, name),
			Namespace: k.Namespace,
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: name,
				ServicePort: intstr.FromInt(servicePort),
			},
		},
	}
	_, err = client.Create(&ingress)
	return err
}

func (k *IngressService) Remove(name string) error {
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	err = client.Delete(fmt.Sprintf(ingressNameFmt, name), &metav1.DeleteOptions{PropagationPolicy: &deletePropagation})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *IngressService) getClient() (kubernetes.Interface, error) {
	if k.client != nil {
		return k.client, nil
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

func (k *IngressService) ingressClient() (typedV1Beta1.IngressInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.ExtensionsV1beta1().Ingresses(k.Namespace), nil
}
