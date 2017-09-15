// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"

	"github.com/tsuru/ingress-router/ingress"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	typedV1Beta1 "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

// IngressService manages ingresses in a Kubernetes cluster
type IngressService struct {
	*BaseService
}

// Create creates an Ingress resource pointing to a service
// with the same name as the App
func (k *IngressService) Create(appName string) error {
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	i := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ingressName(appName),
			Namespace:   k.Namespace,
			Labels:      map[string]string{appLabel: appName},
			Annotations: make(map[string]string),
		},
		Spec: v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: appName,
				ServicePort: intstr.FromInt(defaultServicePort),
			},
		},
	}
	for k, v := range k.Labels {
		i.ObjectMeta.Labels[k] = v
	}
	for k, v := range k.Annotations {
		i.ObjectMeta.Annotations[k] = v
	}
	_, err = client.Create(&i)
	if k8sErrors.IsAlreadyExists(err) {
		return ingress.ErrIngressAlreadyExists
	}
	return err
}

// Update updates an Ingress resource to point it to either
// the only service or the one responsible for the process web
func (k *IngressService) Update(appName string) error {
	service, err := k.getWebService(appName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient()
	if err != nil {
		return err
	}
	ingress, err := k.get(appName)
	if err != nil {
		return err
	}
	if ingress.Spec.Backend.ServiceName == service.Name {
		return nil
	}
	ingress.Spec.Backend.ServiceName = service.Name
	ingress.Spec.Backend.ServicePort = intstr.FromInt(int(service.Spec.Ports[0].Port))
	_, err = ingressClient.Update(ingress)
	return err
}

// Swap swaps backend services of two applications ingresses
func (k *IngressService) Swap(srcApp, dstApp string) error {
	srcIngress, err := k.get(srcApp)
	if err != nil {
		return err
	}
	dstIngress, err := k.get(dstApp)
	if err != nil {
		return err
	}
	swap(srcIngress, dstIngress)
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	_, err = client.Update(srcIngress)
	if err != nil {
		return err
	}
	_, err = client.Update(dstIngress)
	if err != nil {
		swap(srcIngress, dstIngress)
		_, errRollback := client.Update(srcIngress)
		if errRollback != nil {
			return fmt.Errorf("failed to rollback swap %v: %v", err, errRollback)
		}
		return err
	}
	return nil
}

// Remove removes the Ingress resource associated with the app
func (k *IngressService) Remove(appName string) error {
	ingress, err := k.get(appName)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if dstApp, ok := ingress.Labels[swapLabel]; ok {
		return ErrAppSwapped{App: appName, DstApp: dstApp}
	}
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	deletePropagation := metav1.DeletePropagationForeground
	err = client.Delete(ingressName(appName), &metav1.DeleteOptions{PropagationPolicy: &deletePropagation})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Get gets the address of the loadbalancer associated with
// the app Ingress resource
func (k *IngressService) Get(appName string) (map[string]string, error) {
	ingress, err := k.get(appName)
	if err != nil {
		return nil, err
	}
	var addr string
	lbs := ingress.Status.LoadBalancer.Ingress
	if len(lbs) != 0 {
		addr = lbs[0].IP
	}
	return map[string]string{"address": addr}, nil
}

func (k *IngressService) get(appName string) (*v1beta1.Ingress, error) {
	client, err := k.ingressClient()
	if err != nil {
		return nil, err
	}
	ingress, err := client.Get(ingressName(appName), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return ingress, nil
}

func (k *IngressService) ingressClient() (typedV1Beta1.IngressInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.ExtensionsV1beta1().Ingresses(k.Namespace), nil
}

func ingressName(appName string) string {
	return appName + "-ingress"
}

func swap(srcIngress, dstIngress *v1beta1.Ingress) {
	srcIngress.Spec.Backend.ServiceName, dstIngress.Spec.Backend.ServiceName = dstIngress.Spec.Backend.ServiceName, srcIngress.Spec.Backend.ServiceName
	srcIngress.Spec.Backend.ServicePort, dstIngress.Spec.Backend.ServicePort = dstIngress.Spec.Backend.ServicePort, srcIngress.Spec.Backend.ServicePort
	if srcIngress.Labels[swapLabel] == dstIngress.Labels[appLabel] {
		delete(srcIngress.Labels, swapLabel)
		delete(dstIngress.Labels, swapLabel)
	} else {
		srcIngress.Labels[swapLabel] = dstIngress.Labels[appLabel]
		dstIngress.Labels[swapLabel] = srcIngress.Labels[appLabel]
	}
}
