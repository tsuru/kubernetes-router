// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	typedV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	typedV1Beta1 "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
	v1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/pkg/apis/extensions/v1beta1"
)

var (
	// AnnotationsPrefix defines the common prefix used in the nginx ingress controller
	AnnotationsPrefix = "nginx.ingress.kubernetes.io"
	// AnnotationsNginx defines the common prefix used in the nginx ingress controller
	AnnotationsNginx = map[string]string{"kubernetes.io/ingress.class": "nginx"}
)

// IngressNginxService manages ingresses in a Kubernetes cluster that uses ingress-nginx
type IngressNginxService struct {
	*BaseService
}

// Create creates an Ingress resource pointing to a service
// with the same name as the App
func (k *IngressNginxService) Create(appName string, routerOpts router.Opts) error {
	var spec v1beta1.IngressSpec
	client, err := k.ingressClient()
	if err != nil {
		return err
	}
	if len(routerOpts.Domain) > 0 {
		spec = v1beta1.IngressSpec{
			Rules: []v1beta1.IngressRule{
				{
					Host: routerOpts.Domain,
					IngressRuleValue: v1beta1.IngressRuleValue{
						HTTP: &v1beta1.HTTPIngressRuleValue{
							Paths: []v1beta1.HTTPIngressPath{
								{
									Path: routerOpts.Route,
									Backend: v1beta1.IngressBackend{
										ServiceName: appName,
										ServicePort: intstr.FromInt(defaultServicePort),
									},
								},
							},
						},
					},
				},
			},
		}
	} else {
		spec = v1beta1.IngressSpec{
			Backend: &v1beta1.IngressBackend{
				ServiceName: appName,
				ServicePort: intstr.FromInt(defaultServicePort),
			},
		}
	}
	i := v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ingressName(appName),
			Namespace:   k.Namespace,
			Labels:      map[string]string{appLabel: appName},
			Annotations: AnnotationsNginx,
		},
		Spec: spec,
	}
	for k, v := range k.Labels {
		i.ObjectMeta.Labels[k] = v
	}
	for k, v := range k.Annotations {
		i.ObjectMeta.Annotations[k] = v
	}
	for k, v := range routerOpts.AdditionalOpts {
		if !strings.Contains(k, "/") {
			i.ObjectMeta.Annotations[annotationWithPrefix(k)] = v
		} else {
			i.ObjectMeta.Annotations[k] = v
		}

	}
	_, err = client.Create(&i)
	if k8sErrors.IsAlreadyExists(err) {
		return router.ErrIngressAlreadyExists
	}
	return err
}

// Update updates an Ingress resource to point it to either
// the only service or the one responsible for the process web
func (k *IngressNginxService) Update(appName string, _ router.Opts) error {
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
	ingress.Spec.Backend.ServiceName = service.Name
	ingress.Spec.Backend.ServicePort = intstr.FromInt(int(service.Spec.Ports[0].Port))
	_, err = ingressClient.Update(ingress)
	return err
}

// Swap swaps backend services of two applications ingresses
func (k *IngressNginxService) Swap(srcApp, dstApp string) error {
	srcIngress, err := k.get(srcApp)
	if err != nil {
		return err
	}
	dstIngress, err := k.get(dstApp)
	if err != nil {
		return err
	}
	k.swap(srcIngress, dstIngress)
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
		k.swap(srcIngress, dstIngress)
		_, errRollback := client.Update(srcIngress)
		if errRollback != nil {
			return fmt.Errorf("failed to rollback swap %v: %v", err, errRollback)
		}
	}
	return err
}

// Remove removes the Ingress resource associated with the app
func (k *IngressNginxService) Remove(appName string) error {
	ingress, err := k.get(appName)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if dstApp, swapped := k.BaseService.isSwapped(ingress.ObjectMeta); swapped {
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
func (k *IngressNginxService) Get(appName string) (map[string]string, error) {
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

func (k *IngressNginxService) get(appName string) (*v1beta1.Ingress, error) {
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

func (k *IngressNginxService) ingressClient() (typedV1Beta1.IngressInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.ExtensionsV1beta1().Ingresses(k.Namespace), nil
}

func (k *IngressNginxService) secretClient() (typedV1.SecretInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.CoreV1().Secrets(k.Namespace), nil
}

func secretName(appName string) string {
	return appName + "-secret"
}

func annotationWithPrefix(suffix string) string {
	return fmt.Sprintf("%v/%v", AnnotationsPrefix, suffix)
}

func (k *IngressNginxService) swap(srcIngress, dstIngress *v1beta1.Ingress) {
	srcIngress.Spec.Backend.ServiceName, dstIngress.Spec.Backend.ServiceName = dstIngress.Spec.Backend.ServiceName, srcIngress.Spec.Backend.ServiceName
	srcIngress.Spec.Backend.ServicePort, dstIngress.Spec.Backend.ServicePort = dstIngress.Spec.Backend.ServicePort, srcIngress.Spec.Backend.ServicePort
	k.BaseService.swap(&srcIngress.ObjectMeta, &dstIngress.ObjectMeta)
}

// AddCertificate adds certificates to app ingress
func (k *IngressNginxService) AddCertificate(appName string, certName string, cert router.CertData) error {
	ingressClient, err := k.ingressClient()
	if err != nil {
		return err
	}
	secret, err := k.secretClient()
	if err != nil {
		return err
	}
	ingress, err := k.get(appName)
	if err != nil {
		return err
	}

	tlsSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName(appName),
			Namespace:   k.Namespace,
			Labels:      map[string]string{appLabel: appName},
			Annotations: make(map[string]string),
		},
		Type: "kubernetes.io/tls",
		StringData: map[string]string{
			"tls.key": cert.Key,
			"tls.crt": cert.Certificate,
		},
	}
	retSecret, err := secret.Create(&tlsSecret)
	if err != nil {
		return err
	}

	ingress.Spec.TLS = []v1beta1.IngressTLS{
		{
			Hosts:      []string{ingress.Spec.Rules[0].Host},
			SecretName: retSecret.Name,
		},
	}
	_, err = ingressClient.Update(ingress)
	return err
}

// GetCertificate get certificates from app ingress
func (k *IngressNginxService) GetCertificate(appName string, certName string) (*router.CertData, error) {
	secret, err := k.secretClient()
	if err != nil {
		return nil, err
	}

	retSecret, err := secret.Get(secretName(appName), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// string(retSecret.Data["tls.crt"][:bytes.IndexByte(retSecret.Data["tls.crt"], 0)])
	certificate := fmt.Sprintf("%s", retSecret.Data["tls.crt"])
	// string(retSecret.Data["tls.key"][:bytes.IndexByte(retSecret.Data["tls.key"], 0)])
	key := fmt.Sprintf("%s", retSecret.Data["tls.key"])
	return &router.CertData{Certificate: certificate, Key: key}, err
}

// RemoveCertificate delete certificates from app ingress
func (k *IngressNginxService) RemoveCertificate(appName string, certName string) error {
	ingressClient, err := k.ingressClient()
	if err != nil {
		return err
	}
	ingress, err := k.get(appName)
	if err != nil {
		return err
	}
	secret, err := k.secretClient()
	if err != nil {
		return err
	}

	ingress.Spec.TLS = nil
	_, err = ingressClient.Update(ingress)
	if err != nil {
		return err
	}

	err = secret.Delete(secretName(appName), &metav1.DeleteOptions{})

	return err
}

// SetCname adds CNAME to app ingress
func (k *IngressNginxService) SetCname(appName string, cname string) error {
	ingressClient, err := k.ingressClient()
	if err != nil {
		return err
	}
	ingress, err := k.get(appName)
	if err != nil {
		return err
	}

	annotations := ingress.GetAnnotations()
	aliases, ok := annotations[annotationWithPrefix("server-alias")]
	if !ok {
		aliases = cname
	} else {
		aliasesArray := strings.Split(aliases, " ")
		for _, v := range aliasesArray {
			if strings.Compare(v, cname) == 0 {
				return errors.New("cname already exists")
			}
		}
		aliasesArray = append(aliasesArray, []string{cname}...)
		aliases = strings.Join(aliasesArray, " ")
	}
	annotations[annotationWithPrefix("server-alias")] = aliases
	ingress.SetAnnotations(annotations)

	_, err = ingressClient.Update(ingress)

	return err
}

// GetCnames get CNAMEs from app ingress
func (k *IngressNginxService) GetCnames(appName string) (*router.CnamesResp, error) {
	ingress, err := k.get(appName)
	if err != nil {
		return nil, err
	}

	aliases, ok := ingress.GetAnnotations()[annotationWithPrefix("server-alias")]
	if !ok {
		return &router.CnamesResp{}, err
	}

	return &router.CnamesResp{Cnames: strings.Split(aliases, " ")}, err
}

// UnsetCname delete CNAME from app ingress
func (k *IngressNginxService) UnsetCname(appName string, cname string) error {
	ingressClient, err := k.ingressClient()
	if err != nil {
		return err
	}
	ingress, err := k.get(appName)
	if err != nil {
		return err
	}

	annotations := ingress.GetAnnotations()
	aliases := strings.Split(annotations[annotationWithPrefix("server-alias")], " ")

	for index, value := range aliases {
		if strings.Compare(value, cname) == 0 {
			aliases = append(aliases[:index], aliases[index+1:]...)
			break
		}
	}

	annotations[annotationWithPrefix("server-alias")] = strings.Join(aliases, " ")
	ingress.SetAnnotations(annotations)

	_, err = ingressClient.Update(ingress)

	return err
}
