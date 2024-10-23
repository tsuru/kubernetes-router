// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"fmt"
	"log"
	"net"
	"reflect"
	"strconv"
	"strings"

	"github.com/opentracing/opentracing-go"
	"github.com/pkg/errors"
	"github.com/tsuru/kubernetes-router/router"
	v1 "k8s.io/api/core/v1"
	networkingV1 "k8s.io/api/networking/v1"

	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	typedV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	networkingTypedV1 "k8s.io/client-go/kubernetes/typed/networking/v1"
)

var (
	// AnnotationsACMEKey defines the common annotation used to enable acme-tls
	AnnotationsACMEKey = "kubernetes.io/tls-acme"
	labelCNameIngress  = "router.tsuru.io/is-cname-ingress"
	AnnotationsCNames  = "router.tsuru.io/cnames"
	AnnotationFreeze   = "router.tsuru.io/freeze"

	defaultClassOpt          = "class"
	defaultOptsAsAnnotations = map[string]string{
		defaultClassOpt: "kubernetes.io/ingress.class",
	}
	defaultOptsAsAnnotationsDocs = map[string]string{
		defaultClassOpt: "Ingress class for the Ingress object",
	}

	certManagerIssuerKey        = "cert-manager.io/issuer"
	certManagerClusterIssuerKey = "cert-manager.io/cluster-issuer"
	certManagerIssuerKindKey    = "cert-manager.io/issuer-kind"
	certManagerIssuerGroupKey   = "cert-manager.io/issuer-group"
	certManagerCommonName       = "cert-manager.io/common-name"

	certManagerAnnotations = []string{
		certManagerIssuerKey,
		certManagerClusterIssuerKey,
		certManagerIssuerKindKey,
		certManagerIssuerGroupKey,
	}
)

var (
	_ router.Router       = &IngressService{}
	_ router.RouterTLS    = &IngressService{}
	_ router.RouterStatus = &IngressService{}
)

// Cert-manager types
type CertManagerIssuerType int

const (
	certManagerIssuerTypeIssuer = iota
	certManagerIssuerTypeClusterIssuer
	certManagerIssuerTypeExternalIssuer
)

type CertManagerIssuerData struct {
	name       string
	kind       string
	group      string
	issuerType CertManagerIssuerType
}

const (
	errIssuerNotFound         = "issuer %s not found"
	errExternalIssuerNotFound = "external issuer %s not found, err: %s"
	errExternalIssuerInvalid  = "invalid external issuer: %s (requires <resource name>.<resource kind>.<resource group>)"
)

// IngressService manages ingresses in a Kubernetes cluster that uses ingress-nginx
type IngressService struct {
	*BaseService
	DomainSuffix string

	// AnnotationsPrefix defines the common prefix used in the nginx ingress controller
	AnnotationsPrefix string
	// IngressClass defines the default ingress class used by the controller
	IngressClass          string
	UseIngressClassName   bool
	HTTPPort              int
	OptsAsAnnotations     map[string]string
	OptsAsAnnotationsDocs map[string]string
}

// Ensure creates or updates an Ingress resource to point it to either
// the only service or the one responsible for the process web
func (k *IngressService) Ensure(ctx context.Context, id router.InstanceID, o router.EnsureBackendOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "ensureIngress")
	defer span.Finish()

	span.SetTag("cnames", o.CNames)

	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	isNew := false
	existingIngress, err := k.get(ctx, id)
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
		isNew = true
	}

	if !isNew && existingIngress != nil {
		if existingIngress.Annotations[AnnotationFreeze] == "true" {
			log.Printf("Ingress is frozen, skipping: %s/%s", existingIngress.Namespace, existingIngress.Name)
			return nil
		}
	}

	backendTargets, err := k.getBackendTargets(o.Prefixes, o.Opts.ExposeAllServices)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	for k, v := range backendTargets {
		span.SetTag(fmt.Sprintf("%sTarget.service", k), v.Service)
		span.SetTag(fmt.Sprintf("%sTarget.namespace", k), v.Namespace)
	}

	backendServices := map[string]*v1.Service{}
	for key, target := range backendTargets {
		backendServices[key], err = k.getWebService(ctx, id.AppName, target)
		if err != nil {
			setSpanError(span, err)
			return err
		}
	}

	domainSuffix := o.Opts.DomainSuffix
	if k.DomainSuffix != "" {
		domainSuffix = k.DomainSuffix
	}

	vhosts := map[string]string{}
	for prefixString := range backendServices {
		prefix := ""
		if prefixString != "default" {
			prefix = prefixString + "."
		}
		if len(o.Opts.Domain) > 0 {
			vhosts[prefixString] = fmt.Sprintf("%s%s", prefix, o.Opts.Domain)
		} else if o.Opts.DomainPrefix == "" {
			vhosts[prefixString] = fmt.Sprintf("%s%s.%s", prefix, id.AppName, domainSuffix)
		} else {
			vhosts[prefixString] = fmt.Sprintf("%s%s.%s.%s", prefix, o.Opts.DomainPrefix, id.AppName, domainSuffix)
		}
	}

	ingress := &networkingV1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.ingressName(id),
			Namespace: ns,
			Labels: map[string]string{
				appBaseServiceNamespaceLabel: backendTargets["default"].Namespace,
				appBaseServiceNameLabel:      backendTargets["default"].Service,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(backendServices["default"], schema.GroupVersionKind{
					Group:   v1.SchemeGroupVersion.Group,
					Version: v1.SchemeGroupVersion.Version,
					Kind:    "Service",
				}),
			},
		},
		Spec: buildIngressSpec(vhosts, o.Opts.Route, backendServices, k),
	}
	k.fillIngressMeta(ingress, o.Opts, id, o.Team, o.Tags)
	if o.Opts.Acme {
		k.fillIngressTLS(ingress, id)
		ingress.ObjectMeta.Annotations[AnnotationsACMEKey] = "true"
	} else {
		k.cleanupCertManagerAnnotations(ingress)
	}
	if len(o.CNames) > 0 {
		ingress.Annotations[AnnotationsCNames] = strings.Join(o.CNames, ",")
	}

	if isNew {
		_, err = ingressClient.Create(ctx, ingress, metav1.CreateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
	} else if ingressHasChanges(span, existingIngress, ingress) {
		err = k.mergeIngresses(ctx, ingress, existingIngress, id, ingressClient, span)
		if err != nil {
			setSpanError(span, err)
			return err
		}
	}

	var existingCNames []string
	if existingIngress != nil {
		existingCNames = strings.Split(existingIngress.Annotations[AnnotationsCNames], ",")
	}
	_, cnamesToRemove := diffCNames(existingCNames, o.CNames)

	for _, cname := range o.CNames {
		err = k.ensureCNameBackend(ctx, ensureCNameBackendOpts{
			namespace:  ns,
			id:         id,
			parent:     ingress,
			cname:      cname,
			team:       o.Team,
			certIssuer: o.CertIssuers[cname],
			service:    backendServices["default"],
			routerOpts: o.Opts,
			tags:       o.Tags,
		})
		if err != nil {
			err = errors.Wrapf(err, "could not ensure CName: %q", cname)
			setSpanError(span, err)
			return err
		}
	}

	span.LogKV("cnamesToRemove", cnamesToRemove)
	for _, cname := range cnamesToRemove {
		err = k.removeCNameBackend(ctx, ensureCNameBackendOpts{
			namespace:  ns,
			id:         id,
			cname:      cname,
			team:       o.Team,
			certIssuer: o.CertIssuers[cname],
			service:    backendServices["default"],
			routerOpts: o.Opts,
		})
		if err != nil {
			err = errors.Wrapf(err, "could not remove CName: %q", cname)
			setSpanError(span, err)
			return err
		}
	}

	return nil
}

func (k *IngressService) mergeIngresses(ctx context.Context, ingress *networkingV1.Ingress, existingIngress *networkingV1.Ingress, id router.InstanceID, ingressClient networkingTypedV1.IngressInterface, span opentracing.Span) error {
	ingress.ObjectMeta.ResourceVersion = existingIngress.ObjectMeta.ResourceVersion
	if existingIngress.Spec.DefaultBackend != nil {
		ingress.Spec.DefaultBackend = existingIngress.Spec.DefaultBackend
	}

	if existingIngress.Spec.TLS != nil && len(existingIngress.Spec.TLS) > 0 && !isManagedByCertManager(existingIngress.Annotations) {
		k.fillIngressTLS(ingress, id)
	}
	_, err := ingressClient.Update(ctx, ingress, metav1.UpdateOptions{})
	if err != nil {
		setSpanError(span, err)
		return err
	}
	return nil
}

func buildIngressSpec(hosts map[string]string, path string, services map[string]*v1.Service, k *IngressService) networkingV1.IngressSpec {
	pathType := networkingV1.PathTypeImplementationSpecific
	rules := []networkingV1.IngressRule{}
	for k, service := range services {
		r := networkingV1.IngressRule{
			Host: hosts[k],
			IngressRuleValue: networkingV1.IngressRuleValue{
				HTTP: &networkingV1.HTTPIngressRuleValue{
					Paths: []networkingV1.HTTPIngressPath{
						{
							Path:     path,
							PathType: &pathType,
							Backend: networkingV1.IngressBackend{
								Service: &networkingV1.IngressServiceBackend{
									Name: service.Name,
									Port: networkingV1.ServiceBackendPort{
										Number: service.Spec.Ports[0].Port,
									},
								},
							},
						},
					},
				},
			},
		}

		rules = append(rules, r)
	}

	if k.IngressClass != "" && k.UseIngressClassName {
		className := k.IngressClass
		return networkingV1.IngressSpec{
			IngressClassName: &className,
			Rules:            rules,
		}
	}

	return networkingV1.IngressSpec{
		Rules: rules,
	}
}

func setSpanError(span opentracing.Span, err error) {
	span.SetTag("error", true)
	span.LogKV("error.message", err.Error())
}

type ensureCNameBackendOpts struct {
	namespace  string
	id         router.InstanceID
	cname      string
	team       string
	certIssuer string
	parent     *networkingV1.Ingress
	service    *v1.Service
	routerOpts router.Opts
	tags       []string
}

func (k *IngressService) ensureCNameBackend(ctx context.Context, opts ensureCNameBackendOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "ensureIngressCName")
	defer span.Finish()

	span.SetTag("cname", opts.cname)

	ingressClient, err := k.ingressClient(opts.namespace)
	if err != nil {
		return err
	}
	isNew := false
	existingIngress, err := ingressClient.Get(ctx, k.ingressCName(opts.id, opts.cname), metav1.GetOptions{})
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			return err

		}
		isNew = true
	}

	if !isNew && existingIngress != nil {
		if existingIngress.Annotations[AnnotationFreeze] == "true" {
			log.Printf("Ingress is frozen, skipping: %s/%s", existingIngress.Namespace, existingIngress.Name)
			return nil
		}
	}
	ingress := &networkingV1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.ingressCName(opts.id, opts.cname),
			Namespace: opts.namespace,
			Labels: map[string]string{
				appBaseServiceNamespaceLabel: opts.service.Namespace,
				appBaseServiceNameLabel:      opts.service.Name,
				labelCNameIngress:            "true",
			},

			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(opts.parent, schema.GroupVersionKind{
					Group:   networkingV1.SchemeGroupVersion.Group,
					Version: networkingV1.SchemeGroupVersion.Version,
					Kind:    "Ingress",
				}),
			},
		},
		Spec: buildIngressSpec(map[string]string{"ensureCnameBackend": opts.cname}, opts.routerOpts.Route, map[string]*v1.Service{"ensureCnameBackend": opts.service}, k),
	}

	k.fillIngressMeta(ingress, opts.routerOpts, opts.id, opts.team, opts.tags)

	if opts.routerOpts.HTTPOnly {
		k.cleanupCertManagerAnnotations(ingress)
	} else if opts.routerOpts.AcmeCName {
		k.fillIngressTLS(ingress, opts.id)
		ingress.ObjectMeta.Annotations[AnnotationsACMEKey] = "true"
	} else {
		err = k.ensureCNAMECertManagerIssuer(ctx, opts, ingress)
		if err != nil {
			return err
		}
	}

	if isNew {
		_, err = ingressClient.Create(ctx, ingress, metav1.CreateOptions{})
		return err
	}

	if ingressHasChanges(span, existingIngress, ingress) {
		err = k.mergeIngresses(ctx, ingress, existingIngress, opts.id, ingressClient, span)
		if err != nil {
			return err
		}
	}

	if len(ingress.Spec.TLS) == 0 {
		certificateName := k.secretName(opts.id, opts.cname)
		return k.ensureCertmanagerCertificateDeleted(ctx, opts.namespace, certificateName)
	}

	return nil
}

func (k *IngressService) ensureCertmanagerCertificateDeleted(ctx context.Context, namespace, certificateName string) error {
	certManagerClient, err := k.getCertManagerClient()
	if err != nil {
		return err
	}

	err = certManagerClient.CertmanagerV1().Certificates(namespace).Delete(ctx, certificateName, metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}

	return nil
}

func (k *IngressService) ensureCNAMECertManagerIssuer(ctx context.Context, opts ensureCNameBackendOpts, ingress *networkingV1.Ingress) error {
	if opts.certIssuer == "" {
		// If no cert issuer is provided, we should remove any existing cert issuer annotation
		k.cleanupCertManagerAnnotations(ingress)
	} else {
		// If a cert issuer is provided, we should add it to the ingress
		k.fillIngressTLS(ingress, opts.id)
		ingress.ObjectMeta.Annotations[certManagerClusterIssuerKey] = opts.certIssuer

		certIssuerData, err := k.getCertManagerIssuerData(ctx, opts.certIssuer, opts.namespace)
		if err != nil {
			log.Printf("Error getting cert manager issuer data: %v", err)
			return err
		}

		log.Printf("Cert manager issuer data: %v", certIssuerData)

		// Remove previous cermanager annotations if needed and
		// add cert-manager annotations to the ingress.
		k.cleanupCertManagerAnnotations(ingress)

		ingress.Annotations[certManagerCommonName] = opts.cname

		switch certIssuerData.issuerType {

		case certManagerIssuerTypeIssuer:
			ingress.ObjectMeta.Annotations[certManagerIssuerKey] = certIssuerData.name

		case certManagerIssuerTypeClusterIssuer:
			ingress.ObjectMeta.Annotations[certManagerClusterIssuerKey] = certIssuerData.name

		case certManagerIssuerTypeExternalIssuer:
			ingress.ObjectMeta.Annotations[certManagerIssuerKey] = certIssuerData.name
			ingress.ObjectMeta.Annotations[certManagerIssuerKindKey] = certIssuerData.kind
			ingress.ObjectMeta.Annotations[certManagerIssuerGroupKey] = certIssuerData.group
		}
	}

	return nil
}

func (k *IngressService) cleanupCertManagerAnnotations(ingress *networkingV1.Ingress) {
	for _, annotation := range certManagerAnnotations {
		delete(ingress.Annotations, annotation)
	}
}

func (k *IngressService) removeCNameBackend(ctx context.Context, opts ensureCNameBackendOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "removeIngressCName")
	defer span.Finish()

	span.SetTag("cname", opts.cname)

	ingressClient, err := k.ingressClient(opts.namespace)
	if err != nil {
		return err
	}
	err = ingressClient.Delete(ctx, k.ingressCName(opts.id, opts.cname), metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}
	return nil
}

// Remove removes the Ingress resource associated with the app
func (k *IngressService) Remove(ctx context.Context, id router.InstanceID) error {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}
	client, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	deletePropagation := metav1.DeletePropagationForeground
	err = client.Delete(ctx, k.ingressName(id), metav1.DeleteOptions{PropagationPolicy: &deletePropagation})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Get gets the address of the loadbalancer associated with
// the app Ingress resource
func (k *IngressService) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	ingress, err := k.get(ctx, id)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return []string{""}, nil
		}
		return nil, err
	}
	hosts := []string{}
	urls := []string{}
	for _, rule := range ingress.Spec.Rules {
		if k.HTTPPort == 0 {
			hosts = append(hosts, rule.Host)
		} else {
			hostPort := net.JoinHostPort(rule.Host, strconv.Itoa(k.HTTPPort))
			hosts = append(hosts, hostPort)
		}
	}
	for _, hostTLS := range ingress.Spec.TLS {
		for _, h := range hostTLS.Hosts {
			urls = append(urls, fmt.Sprintf("https://%v", h))
		}
	}
	if len(urls) > 0 {
		return urls, nil
	}
	return hosts, nil
}
func (k *IngressService) GetStatus(ctx context.Context, id router.InstanceID) (router.BackendStatus, string, error) {
	ingress, err := k.get(ctx, id)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return router.BackendStatusNotReady, "waiting for deploy", nil
		}
		return router.BackendStatusNotReady, "", err
	}
	if isIngressReady(ingress) {
		return router.BackendStatusReady, "", nil
	}
	detail, err := k.getStatusForRuntimeObject(ctx, ingress.Namespace, "Ingress", ingress.UID)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}

	return router.BackendStatusNotReady, detail, nil
}

func (k *IngressService) get(ctx context.Context, id router.InstanceID) (*networkingV1.Ingress, error) {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	client, err := k.ingressClient(ns)
	if err != nil {
		return nil, err
	}
	ingress, err := client.Get(ctx, k.ingressName(id), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return ingress, nil
}

func (k *IngressService) ingressClient(namespace string) (networkingTypedV1.IngressInterface, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	return client.NetworkingV1().Ingresses(namespace), nil
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

func (s *IngressService) ingressCName(id router.InstanceID, cname string) string {
	return s.hashedResourceName(id, "kubernetes-router-cname-"+cname, 253)
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

// AddCertificate adds certificates to app ingress
func (k *IngressService) AddCertificate(ctx context.Context, id router.InstanceID, certCname string, cert router.CertData) error {
	ns, err := k.getAppNamespace(ctx, id.AppName)
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
	ingress, err := k.targetIngressForCertificate(ctx, id, certCname)
	if err != nil {
		return err
	}

	if isManagedByCertManager(ingress.Annotations) {
		return fmt.Errorf("cannot add certificate to ingress %s, it is managed by cert-manager", ingress.Name)
	}

	foundCname := false
	foundCNames := []string{}
	for _, rules := range ingress.Spec.Rules {
		foundCNames = append(foundCNames, rules.Host)

		if rules.Host == certCname {
			foundCname = true
			break
		}
	}

	if !foundCname {
		return fmt.Errorf("cname %s is not found in ingress %s, found cnames: %s", certCname, ingress.Name, strings.Join(foundCNames, ", "))
	}

	if ingress.Annotations[AnnotationsACMEKey] == "true" {
		return fmt.Errorf("cannot add certificate to ingress %s, it is managed by ACME", ingress.Name)
	}

	secretName := k.secretName(id, certCname)
	tlsSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
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
	_, err = secret.Create(ctx, &tlsSecret, metav1.CreateOptions{})

	if k8sErrors.IsAlreadyExists(err) {
		var existingSecret *v1.Secret
		existingSecret, err = secret.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		tlsSecret.ResourceVersion = existingSecret.ResourceVersion
		_, err = secret.Update(ctx, &tlsSecret, metav1.UpdateOptions{})
	}

	if err != nil {
		return err
	}

	tlsSpecExists := false
	for index, ingressTLS := range ingress.Spec.TLS {
		if ingressTLS.SecretName == tlsSecret.Name {
			ingress.Spec.TLS[index].Hosts = []string{certCname}
			tlsSpecExists = true
			break
		}
	}

	if !tlsSpecExists {
		ingress.Spec.TLS = append(ingress.Spec.TLS,
			[]networkingV1.IngressTLS{
				{
					Hosts:      []string{certCname},
					SecretName: tlsSecret.Name,
				},
			}...)
	}
	_, err = ingressClient.Update(ctx, ingress, metav1.UpdateOptions{})
	return err
}

func (k *IngressService) targetIngressForCertificate(ctx context.Context, id router.InstanceID, certCname string) (*networkingV1.Ingress, error) {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return nil, err
	}
	ingressCName, err := ingressClient.Get(ctx, k.ingressCName(id, certCname), metav1.GetOptions{})
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			return nil, err
		}
	}
	if ingressCName != nil && ingressCName.Labels[appLabel] == id.AppName {
		return ingressCName, nil
	}
	return k.get(ctx, id)
}

// GetCertificate get certificates from app ingress
func (k *IngressService) GetCertificate(ctx context.Context, id router.InstanceID, certCname string) (*router.CertData, error) {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	secret, err := k.secretClient(ns)
	if err != nil {
		return nil, err
	}

	retSecret, err := secret.Get(ctx, k.secretName(id, certCname), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	certificate := string(retSecret.Data["tls.crt"])
	key := string(retSecret.Data["tls.key"])
	return &router.CertData{Certificate: certificate, Key: key}, err
}

// RemoveCertificate delete certificates from app ingress
func (k *IngressService) RemoveCertificate(ctx context.Context, id router.InstanceID, certCname string) error {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}
	ingressClient, err := k.ingressClient(ns)
	if err != nil {
		return err
	}
	ingress, err := k.targetIngressForCertificate(ctx, id, certCname)
	if err != nil {
		return err
	}
	if ingress.Annotations[AnnotationsACMEKey] == "true" {
		return fmt.Errorf("cannot remove certificate from ingress %s, it is managed by ACME", ingress.Name)
	}

	if isManagedByCertManager(ingress.Annotations) {
		return fmt.Errorf("cannot remove certificate to ingress %s, it is managed by cert-manager", ingress.Name)
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
	_, err = ingressClient.Update(ctx, ingress, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	err = secret.Delete(ctx, k.secretName(id, certCname), metav1.DeleteOptions{})
	return err
}

// SupportedOptions returns the supported options
func (s *IngressService) SupportedOptions(ctx context.Context) map[string]string {
	opts := map[string]string{
		router.Domain:      "",
		router.Acme:        "",
		router.Route:       "",
		router.AllPrefixes: "",
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

func (s *IngressService) fillIngressMeta(i *networkingV1.Ingress, routerOpts router.Opts, id router.InstanceID, team string, tags []string) {
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
	i.ObjectMeta.Labels[teamLabel] = team

	additionalOpts := routerOpts.AdditionalOpts
	if s.IngressClass != "" && !s.UseIngressClassName {
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

	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		var key, value string
		if len(parts) != 2 {
			continue
		}

		key = parts[0]
		value = parts[1]

		if key == "" {
			continue
		}
		labelName := customTagPrefixLabel + key
		if len(validation.IsQualifiedName(labelName)) > 0 {
			// Ignoring tags that are not valid identifiers for labels or annotations
			continue
		}
		i.ObjectMeta.Labels[labelName] = value
	}
}

func (s *IngressService) validateCustomIssuer(ctx context.Context, resource CertManagerIssuerData, ns string) error {
	sigsClient, err := s.getSigsClient()
	if err != nil {
		return err
	}

	mapping, err := sigsClient.RESTMapper().RESTMapping(schema.GroupKind{
		Group: resource.group,
		Kind:  resource.kind,
	})
	if err != nil {
		return err
	}

	u := &unstructured.Unstructured{}
	u.Object = map[string]interface{}{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   mapping.GroupVersionKind.Group,
		Kind:    mapping.GroupVersionKind.Kind,
		Version: mapping.GroupVersionKind.Version,
	})

	err = sigsClient.Get(ctx, types.NamespacedName{
		Name:      resource.name,
		Namespace: ns,
	}, u)
	if err != nil {
		return err
	}

	return nil
}

func (s *IngressService) getCertManagerIssuerData(ctx context.Context, issuerName, namespace string) (CertManagerIssuerData, error) {
	if strings.Contains(issuerName, ".") {
		// Treat as external issuer since it's more general
		parts := strings.SplitN(issuerName, ".", 3)
		if len(parts) != 3 {
			return CertManagerIssuerData{}, fmt.Errorf(errExternalIssuerInvalid, issuerName)
		}
		cmIssuerData := CertManagerIssuerData{
			name:       parts[0],
			kind:       parts[1],
			group:      parts[2],
			issuerType: certManagerIssuerTypeExternalIssuer,
		}

		if err := s.validateCustomIssuer(ctx, cmIssuerData, namespace); err != nil {
			return CertManagerIssuerData{}, fmt.Errorf(errExternalIssuerNotFound, issuerName, err.Error())
		}

		return cmIssuerData, nil
	}

	// Treat as CertManager issuer
	cmClient, err := s.getCertManagerClient()
	if err != nil {
		return CertManagerIssuerData{}, err
	}

	_, err = cmClient.CertmanagerV1().Issuers(namespace).Get(ctx, issuerName, metav1.GetOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return CertManagerIssuerData{}, err
	}

	if err == nil {
		return CertManagerIssuerData{
			name:       issuerName,
			issuerType: certManagerIssuerTypeIssuer,
		}, nil
	}

	// Check if it's a cluster issuer
	_, err = cmClient.CertmanagerV1().ClusterIssuers().Get(ctx, issuerName, metav1.GetOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return CertManagerIssuerData{}, err
	}

	if err == nil {
		return CertManagerIssuerData{
			name:       issuerName,
			issuerType: certManagerIssuerTypeClusterIssuer,
		}, nil
	}

	// Issuer not found
	return CertManagerIssuerData{}, fmt.Errorf(errIssuerNotFound, issuerName)
}

func (s *IngressService) fillIngressTLS(i *networkingV1.Ingress, id router.InstanceID) {
	tlsRules := []networkingV1.IngressTLS{}
	if len(i.Spec.Rules) > 0 {
		for _, rule := range i.Spec.Rules {
			tlsRules = append(tlsRules, networkingV1.IngressTLS{
				Hosts:      []string{rule.Host},
				SecretName: s.secretName(id, rule.Host),
			})
		}
	}
	i.Spec.TLS = tlsRules
}

func ingressHasChanges(span opentracing.Span, existing *networkingV1.Ingress, ing *networkingV1.Ingress) (hasChanges bool) {
	if !reflect.DeepEqual(existing.Spec, ing.Spec) {
		span.LogKV(
			"message", "ingress has changed the spec",
			"ingress", existing.Name,
		)
		return true
	}

	if existing.Annotations[AnnotationsCNames] != ing.Annotations[AnnotationsCNames] {
		return true
	}

	for key, value := range ing.Annotations {
		if existing.Annotations[key] != value {
			span.LogKV(
				"message", "ingress has changed the annotation",
				"ingress", existing.Name,
				"annotation", key,
				"existingValue", existing.Annotations[key],
				"newValue", value,
			)

			return true
		}
	}
	for key, value := range ing.Labels {
		if existing.Labels[key] != value {
			span.LogKV(
				"message", "ingress has changed the label",
				"ingress", existing.Name,
				"label", key,
				"existingValue", existing.Labels[key],
				"newValue", value,
			)
			return true
		}
	}
	span.LogKV(
		"message", "ingress has no changes",
		"ingress", existing.Name,
	)
	return false
}

func isIngressReady(ingress *networkingV1.Ingress) bool {
	if len(ingress.Status.LoadBalancer.Ingress) == 0 {
		return false
	}
	return ingress.Status.LoadBalancer.Ingress[0].IP != "" || ingress.Status.LoadBalancer.Ingress[0].Hostname != ""
}

func isManagedByCertManager(annotations map[string]string) bool {
	for _, annotation := range certManagerAnnotations {
		if _, ok := annotations[annotation]; ok {
			return true
		}
	}
	return false
}
