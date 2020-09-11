// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	typedV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	typedV1beta1 "k8s.io/client-go/kubernetes/typed/extensions/v1beta1"
)

var (
	// AnnotationsACMEKey defines the common annotation used to enable acme-tls
	AnnotationsACMEKey = "kubernetes.io/tls-acme"

	defaultClassOpt          = "class"
	defaultOptsAsAnnotations = map[string]string{
		defaultClassOpt: "kubernetes.io/ingress.class",
	}
	defaultOptsAsAnnotationsDocs = map[string]string{
		defaultClassOpt: "Ingress class for the Ingress object",
	}
)

var (
	_ router.Router      = &IngressService{}
	_ router.RouterCNAME = &IngressService{}
	_ router.RouterTLS   = &IngressService{}
)

// IngressService manages ingresses in a Kubernetes cluster that uses ingress-nginx
type IngressService struct {
	*BaseService
	DefaultDomain string

	// AnnotationsPrefix defines the common prefix used in the nginx ingress controller
	AnnotationsPrefix string
	// IngressClass defines the default ingress class used by the controller
	IngressClass string

	OptsAsAnnotations     map[string]string
	OptsAsAnnotationsDocs map[string]string
}

// Create creates an Ingress resource pointing to a service
// with the same name as the App
func (k *IngressService) Create(ctx context.Context, id router.InstanceID, routerOpts router.Opts) error {
	var spec v1beta1.IngressSpec
	var vhost string
	app, err := k.getApp(id.AppName)
	if err != nil {
		return err
	}
	ns := k.Namespace
	if app != nil {
		ns = app.Spec.NamespaceName
	}
	client, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	if len(routerOpts.Domain) > 0 {
		vhost = routerOpts.Domain
	} else if id.InstanceName == "" {
		vhost = fmt.Sprintf("%v.%v", id.AppName, k.DefaultDomain)
	} else {
		vhost = fmt.Sprintf("%v.instance.%v.%v", id.InstanceName, id.AppName, k.DefaultDomain)
	}
	spec = v1beta1.IngressSpec{
		Rules: []v1beta1.IngressRule{
			{
				Host: vhost,
				IngressRuleValue: v1beta1.IngressRuleValue{
					HTTP: &v1beta1.HTTPIngressRuleValue{
						Paths: []v1beta1.HTTPIngressPath{
							{
								Path: routerOpts.Route,
								Backend: v1beta1.IngressBackend{
									ServiceName: id.AppName,
									ServicePort: intstr.FromInt(defaultServicePort),
								},
							},
						},
					},
				},
			},
		},
	}
	namespace, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	i := &v1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.ingressName(id),
			Namespace: namespace,
		},
		Spec: spec,
	}
	k.fillIngressMeta(i, routerOpts, id)

	i, isNew, err := mergeIngresses(client, i)
	if err != nil {
		return err
	}
	if isNew {
		_, err = client.Create(i)
	} else {
		_, err = client.Update(i)
	}
	return err
}

// Update updates an Ingress resource to point it to either
// the only service or the one responsible for the process web
func (k *IngressService) Update(ctx context.Context, id router.InstanceID, extraData router.RoutesRequestExtraData) error {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.get(id)
	if err != nil {
		return err
	}
	service, err := k.getWebService(id.AppName, extraData, ingress.Labels)
	if err != nil {
		return err
	}
	if extraData.Namespace != "" && extraData.Service != "" {
		ingress.Labels[appBaseServiceNamespaceLabel] = extraData.Namespace
		ingress.Labels[appBaseServiceNameLabel] = extraData.Service
	}
	ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = service.Name
	ingress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort = intstr.FromInt(int(service.Spec.Ports[0].Port))
	_, err = ingressClient.Update(ingress)
	return err
}

// Swap swaps backend services of two applications ingresses
func (k *IngressService) Swap(ctx context.Context, srcApp, dstApp router.InstanceID) error {
	srcIngress, err := k.get(srcApp)
	if err != nil {
		return err
	}
	dstIngress, err := k.get(dstApp)
	if err != nil {
		return err
	}
	k.swap(srcIngress, dstIngress)
	ns, err := k.getAppNamespace(srcApp.AppName)
	if err != nil {
		return err
	}
	ns2, err := k.getAppNamespace(dstApp.AppName)
	if err != nil {
		return err
	}
	if ns != ns2 {
		return fmt.Errorf("unable to swap apps with different namespaces: %v != %v", ns, ns2)
	}
	client, err := k.ingressClient(ns)
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
func (k *IngressService) Remove(ctx context.Context, id router.InstanceID) error {
	ingress, err := k.get(id)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if dstApp, swapped := k.BaseService.isSwapped(ingress.ObjectMeta); swapped {
		return ErrAppSwapped{App: id.AppName, DstApp: dstApp}
	}
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	client, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	deletePropagation := metav1.DeletePropagationForeground
	err = client.Delete(k.ingressName(id), &metav1.DeleteOptions{PropagationPolicy: &deletePropagation})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Get gets the address of the loadbalancer associated with
// the app Ingress resource
func (k *IngressService) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	ingress, err := k.get(id)
	if err != nil {
		return nil, err
	}

	return []string{fmt.Sprintf("%v", ingress.Spec.Rules[0].Host)}, nil
}

func (k *IngressService) get(id router.InstanceID) (*v1beta1.Ingress, error) {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return nil, err
	}
	client, err := k.ingressClient(ns)
	if err != nil {
		return nil, err
	}
	ingress, err := client.Get(k.ingressName(id), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return ingress, nil
}

func (k *IngressService) ingressClient(namespace string) (typedV1beta1.IngressInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.ExtensionsV1beta1().Ingresses(namespace), nil
}

func (k *IngressService) secretClient(namespace string) (typedV1.SecretInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.CoreV1().Secrets(namespace), nil
}

func (s *IngressService) ingressName(id router.InstanceID) string {
	return s.hashedResourceName(id, "kubernetes-router-"+id.AppName+"-ingress", 253)
}

func (s *IngressService) secretName(id router.InstanceID, certName string) string {
	return s.hashedResourceName(id, "kr-"+id.AppName+"-"+certName, 253)
}

func (s *IngressService) annotationWithPrefix(suffix string) string {
	if s.AnnotationsPrefix == "" {
		return suffix
	}
	return fmt.Sprintf("%v/%v", s.AnnotationsPrefix, suffix)
}

func (k *IngressService) swap(srcIngress, dstIngress *v1beta1.Ingress) {
	srcIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName, dstIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName = dstIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName, srcIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServiceName
	srcIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort, dstIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort = dstIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort, srcIngress.Spec.Rules[0].IngressRuleValue.HTTP.Paths[0].Backend.ServicePort
	k.BaseService.swap(&srcIngress.ObjectMeta, &dstIngress.ObjectMeta)
}

// AddCertificate adds certificates to app ingress
func (k *IngressService) AddCertificate(ctx context.Context, id router.InstanceID, certCname string, cert router.CertData) error {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	secret, err := k.secretClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.get(id)
	if err != nil {
		return err
	}
	tlsSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.secretName(id, certCname),
			Namespace: ns,
			Labels: map[string]string{
				appLabel:    id.AppName,
				domainLabel: certCname,
			},
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

	ingress.Spec.TLS = append(ingress.Spec.TLS,
		[]v1beta1.IngressTLS{
			{
				Hosts:      []string{certCname},
				SecretName: retSecret.Name,
			},
		}...)
	_, err = ingressClient.Update(ingress)
	return err
}

// GetCertificate get certificates from app ingress
func (k *IngressService) GetCertificate(ctx context.Context, id router.InstanceID, certCname string) (*router.CertData, error) {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return nil, err
	}
	secret, err := k.secretClient(ns)
	if err != nil {
		return nil, err
	}

	retSecret, err := secret.Get(k.secretName(id, certCname), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	certificate := fmt.Sprintf("%s", retSecret.Data["tls.crt"])
	key := fmt.Sprintf("%s", retSecret.Data["tls.key"])
	return &router.CertData{Certificate: certificate, Key: key}, err
}

// RemoveCertificate delete certificates from app ingress
func (k *IngressService) RemoveCertificate(ctx context.Context, id router.InstanceID, certCname string) error {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.get(id)
	if err != nil {
		return err
	}
	secret, err := k.secretClient(ns)
	if err != nil {
		return err
	}
	for k := range ingress.Spec.TLS {
		for _, host := range ingress.Spec.TLS[k].Hosts {
			if strings.Compare(certCname, host) == 0 {
				ingress.Spec.TLS = append(ingress.Spec.TLS[:k], ingress.Spec.TLS[k+1:]...)
			}
		}
	}
	_, err = ingressClient.Update(ingress)
	if err != nil {
		return err
	}
	err = secret.Delete(k.secretName(id, certCname), &metav1.DeleteOptions{})
	return err
}

// SetCname adds CNAME to app ingress
func (k *IngressService) SetCname(ctx context.Context, id router.InstanceID, cname string) error {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.get(id)
	if err != nil {
		return err
	}
	annotations := ingress.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	aliases, ok := annotations[k.annotationWithPrefix("server-alias")]
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
	annotations[k.annotationWithPrefix("server-alias")] = strings.TrimSpace(aliases)
	ingress.SetAnnotations(annotations)

	if val, ok := annotations[AnnotationsACMEKey]; ok && strings.Contains(val, "true") {
		log.Printf("Acme-tls is enabled on ingress, creating TLS secret for CNAME.")
		ingress.Spec.TLS = append(ingress.Spec.TLS,
			[]v1beta1.IngressTLS{
				{
					Hosts:      []string{cname},
					SecretName: k.secretName(id, cname),
				},
			}...)
	}

	_, err = ingressClient.Update(ingress)

	return err
}

// GetCnames get CNAMEs from app ingress
func (k *IngressService) GetCnames(ctx context.Context, id router.InstanceID) (*router.CnamesResp, error) {
	ingress, err := k.get(id)
	if err != nil {
		return nil, err
	}

	aliases, ok := ingress.GetAnnotations()[k.annotationWithPrefix("server-alias")]
	if !ok {
		return &router.CnamesResp{}, err
	}

	return &router.CnamesResp{Cnames: strings.Split(aliases, " ")}, err
}

// UnsetCname delete CNAME from app ingress
func (k *IngressService) UnsetCname(ctx context.Context, id router.InstanceID, cname string) error {
	ns, err := k.getAppNamespace(id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.get(id)
	if err != nil {
		return err
	}

	annotations := ingress.GetAnnotations()
	aliases := strings.Split(annotations[k.annotationWithPrefix("server-alias")], " ")

	for index, value := range aliases {
		if strings.Compare(value, cname) == 0 {
			aliases = append(aliases[:index], aliases[index+1:]...)
			break
		}
	}

	annotations[k.annotationWithPrefix("server-alias")] = strings.TrimSpace(strings.Join(aliases, " "))
	ingress.SetAnnotations(annotations)

	_, err = ingressClient.Update(ingress)

	return err
}

// SupportedOptions returns the supported options
func (s *IngressService) SupportedOptions(ctx context.Context) map[string]string {
	opts := map[string]string{
		router.Domain: "",
		router.Acme:   "",
		router.Route:  "",
	}
	docs := mergeMaps(defaultOptsAsAnnotationsDocs, s.OptsAsAnnotationsDocs)
	for k, v := range mergeMaps(defaultOptsAsAnnotations, s.OptsAsAnnotations) {
		opts[k] = v
		if docs[k] != "" {
			opts[k] = docs[k]
		}
	}
	return opts
}

func (s *IngressService) fillIngressMeta(i *v1beta1.Ingress, routerOpts router.Opts, id router.InstanceID) {
	if i.ObjectMeta.Labels == nil {
		i.ObjectMeta.Labels = map[string]string{}
	}
	if i.ObjectMeta.Annotations == nil {
		i.ObjectMeta.Annotations = map[string]string{}
	}
	for k, v := range s.Labels {
		i.ObjectMeta.Labels[k] = v
	}
	for k, v := range s.Annotations {
		i.ObjectMeta.Annotations[k] = v
	}
	i.ObjectMeta.Labels[appLabel] = id.AppName

	additionalOpts := routerOpts.AdditionalOpts
	if s.IngressClass != "" {
		additionalOpts = mergeMaps(routerOpts.AdditionalOpts, map[string]string{
			defaultClassOpt: s.IngressClass,
		})
	}

	optsAsAnnotations := mergeMaps(defaultOptsAsAnnotations, s.OptsAsAnnotations)
	for optName, optValue := range additionalOpts {
		labelName, ok := optsAsAnnotations[optName]
		if !ok {
			if strings.Contains(optName, "/") {
				labelName = optName
			} else {
				labelName = s.annotationWithPrefix(optName)
			}
		}
		if strings.HasSuffix(labelName, "-") {
			delete(i.ObjectMeta.Annotations, strings.TrimSuffix(labelName, "-"))
		} else {
			i.ObjectMeta.Annotations[labelName] = optValue
		}
	}
	if !routerOpts.Acme {
		return
	}
	if len(i.Spec.Rules) > 0 {
		i.Spec.TLS = []v1beta1.IngressTLS{
			{
				Hosts:      []string{i.Spec.Rules[0].Host},
				SecretName: s.secretName(id, i.Spec.Rules[0].Host),
			},
		}
	}
	i.ObjectMeta.Annotations[AnnotationsACMEKey] = "true"
}

func mergeIngresses(client typedV1beta1.IngressInterface, ing *v1beta1.Ingress) (*v1beta1.Ingress, bool, error) {
	existing, err := client.Get(ing.Name, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return ing, true, nil
		}
		return nil, false, err
	}

	ing.ObjectMeta.ResourceVersion = existing.ObjectMeta.ResourceVersion
	if existing.Spec.Backend != nil {
		ing.Spec.Backend = existing.Spec.Backend
	}
	return ing, false, nil
}
