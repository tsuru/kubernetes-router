// Copyright 2026 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"errors"
	"fmt"

	"github.com/tsuru/kubernetes-router/router"
	apiNetworking "istio.io/api/networking/v1beta1"
	istionetworking "istio.io/client-go/pkg/apis/networking/v1beta1"
	networkingClientSet "istio.io/client-go/pkg/clientset/versioned/typed/networking/v1beta1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ router.Router = &IstioVirtualService{}

const gatewayReferenceAnnotation = "router.tsuru.io/istio-gateway-reference"

// IstioVirtualService manages application VirtualServices which refer to an
// Istio Gateway managed outside of this router.
type IstioVirtualService struct {
	*BaseService
	istioClient      networkingClientSet.NetworkingV1beta1Interface
	DomainSuffix     string
	GatewayName      string
	GatewayNamespace string
}

func (k *IstioVirtualService) getClient() (networkingClientSet.NetworkingV1beta1Interface, error) {
	if k.istioClient != nil {
		return k.istioClient, nil
	}
	config, err := k.getConfig()
	if err != nil {
		return nil, err
	}
	k.istioClient, err = networkingClientSet.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return k.istioClient, nil
}

func (k *IstioVirtualService) vsName(id router.InstanceID) string {
	return k.hashedResourceName(id, id.AppName, 63)
}

func (k *IstioVirtualService) gatewayHost(id router.InstanceID, domainSuffix string) string {
	if id.InstanceName == "" {
		return fmt.Sprintf("%v.%v", id.AppName, domainSuffix)
	}
	return fmt.Sprintf("%v.instance.%v.%v", id.InstanceName, id.AppName, domainSuffix)
}

func (k *IstioVirtualService) getVS(ctx context.Context, cli networkingClientSet.NetworkingV1beta1Interface, id router.InstanceID) (*istionetworking.VirtualService, error) {
	namespace, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	return cli.VirtualServices(namespace).Get(ctx, k.vsName(id), metav1.GetOptions{})
}

func (k *IstioVirtualService) gatewayReference(name, gatewayNamespace, appNamespace string) string {
	if gatewayNamespace != "" && gatewayNamespace != appNamespace {
		return gatewayNamespace + "/" + name
	}
	return name
}

func (k *IstioVirtualService) updateObjectMeta(result *metav1.ObjectMeta, appName string, opts router.Opts) {
	if result.Labels == nil {
		result.Labels = make(map[string]string)
	}
	if result.Annotations == nil {
		result.Annotations = make(map[string]string)
	}
	for key, value := range k.Labels {
		result.Labels[key] = value
	}
	result.Labels[appLabel] = appName
	for key, value := range k.Annotations {
		result.Annotations[key] = value
	}
	for key, value := range opts.AdditionalOpts {
		result.Annotations[key] = value
	}
}

// updateVirtualService points the managed destination at dstHost. The
// destination previously written by this mode is replaced in place, keeping its
// weight, so unrelated weighted destinations keep their traffic share.
func (k *IstioVirtualService) updateVirtualService(vs *istionetworking.VirtualService, gatewayRef, host, dstHost, previousDstHost string) {
	vs.Spec.Gateways = addToSet(vs.Spec.Gateways, gatewayRef)
	vs.Spec.Hosts = addToSet(vs.Spec.Hosts, host, dstHost)
	if len(vs.Spec.Http) == 0 {
		vs.Spec.Http = append(vs.Spec.Http, &apiNetworking.HTTPRoute{})
	}
	if previousDstHost == dstHost {
		previousDstHost = ""
	}
	route := vs.Spec.Http[0].Route
	dstIdx, previousDstIdx := -1, -1
	for i, dst := range route {
		if dst.Destination == nil {
			continue
		}
		if dstIdx == -1 && dst.Destination.Host == dstHost {
			dstIdx = i
		}
		if previousDstIdx == -1 && previousDstHost != "" && dst.Destination.Host == previousDstHost {
			previousDstIdx = i
		}
	}
	switch {
	case dstIdx != -1 && previousDstIdx != -1:
		route[dstIdx].Weight += route[previousDstIdx].Weight
		route = append(route[:previousDstIdx], route[previousDstIdx+1:]...)
	case previousDstIdx != -1:
		route[previousDstIdx].Destination = &apiNetworking.Destination{Host: dstHost}
	case dstIdx != -1:
		route[dstIdx].Destination = &apiNetworking.Destination{Host: dstHost}
	default:
		route = append(route, &apiNetworking.HTTPRouteDestination{Destination: &apiNetworking.Destination{Host: dstHost}})
	}
	vs.Spec.Http[0].Route = route
	if previousDstHost != "" {
		vs.Spec.Hosts = removeFromSet(vs.Spec.Hosts, previousDstHost)
	}
}

func istioServiceHost(service, namespace, appNamespace string) string {
	if namespace == appNamespace {
		return service
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
}

// Ensure creates or updates the application's VirtualService without managing
// the referenced Istio Gateway.
func (k *IstioVirtualService) Ensure(ctx context.Context, id router.InstanceID, opts router.EnsureBackendOpts) error {
	appNamespace, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}

	gatewayName := k.GatewayName
	if opts.Opts.GatewayName != "" {
		gatewayName = opts.Opts.GatewayName
	}
	if gatewayName == "" {
		return errors.New("gateway name must be specified via istio-virtualservice startup flags or X-Gateway-Name header")
	}
	gatewayNamespace := k.GatewayNamespace
	if opts.Opts.GatewayNamespace != "" {
		gatewayNamespace = opts.Opts.GatewayNamespace
	}
	if gatewayNamespace == "" {
		gatewayNamespace = appNamespace
	}
	gatewayRef := k.gatewayReference(gatewayName, gatewayNamespace, appNamespace)

	defaultTarget, err := k.getDefaultBackendTarget(opts.Prefixes)
	if err != nil {
		return err
	}
	webService, err := k.getWebService(ctx, id.AppName, *defaultTarget)
	if err != nil {
		return err
	}
	cli, err := k.getClient()
	if err != nil {
		return err
	}

	virtualService, err := k.getVS(ctx, cli, id)
	existing := err == nil
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}
	if !existing {
		virtualService = &istionetworking.VirtualService{ObjectMeta: metav1.ObjectMeta{Name: k.vsName(id)}, Spec: apiNetworking.VirtualService{Gateways: []string{"mesh"}}}
	}
	previousDstHost := ""
	if previousService := virtualService.Labels[appBaseServiceNameLabel]; previousService != "" {
		previousNamespace := virtualService.Labels[appBaseServiceNamespaceLabel]
		if previousNamespace == "" {
			previousNamespace = appNamespace
		}
		previousDstHost = istioServiceHost(previousService, previousNamespace, appNamespace)
	}
	// Only detach a reference this mode recorded; a startup default does not
	// prove ownership of an attachment on an existing VirtualService.
	previousRef := virtualService.Annotations[gatewayReferenceAnnotation]
	if previousRef != "" && previousRef != gatewayRef {
		virtualService.Spec.Gateways = removeFromSet(virtualService.Spec.Gateways, previousRef)
	}
	k.updateObjectMeta(&virtualService.ObjectMeta, id.AppName, opts.Opts)
	virtualService.Annotations[gatewayReferenceAnnotation] = gatewayRef
	dstHost := istioServiceHost(webService.Name, defaultTarget.Namespace, appNamespace)
	k.updateVirtualService(virtualService, gatewayRef, k.gatewayHost(id, k.DomainSuffix), dstHost, previousDstHost)
	virtualService.Labels[appBaseServiceNamespaceLabel] = defaultTarget.Namespace
	virtualService.Labels[appBaseServiceNameLabel] = defaultTarget.Service

	existingCNames := hostsFromAnnotation(virtualService.Annotations)
	cnamesToAdd, cnamesToRemove := diffCNames(existingCNames, opts.CNames)
	for _, cname := range cnamesToAdd {
		vsAddHost(virtualService, cname)
	}
	for _, cname := range cnamesToRemove {
		vsRemoveHost(virtualService, cname)
	}

	if existing {
		_, err = cli.VirtualServices(appNamespace).Update(ctx, virtualService, metav1.UpdateOptions{})
	} else {
		_, err = cli.VirtualServices(appNamespace).Create(ctx, virtualService, metav1.CreateOptions{})
	}
	return err
}

func (k *IstioVirtualService) GetAddresses(_ context.Context, id router.InstanceID) ([]string, error) {
	return []string{k.gatewayHost(id, k.DomainSuffix)}, nil
}

func (k *IstioVirtualService) Swap(context.Context, router.InstanceID, router.InstanceID) error {
	return errors.New("swap is not supported, the virtualservice should be edited manually")
}

// Remove detaches the Gateway reference recorded on the VirtualService. A
// missing annotation falls back to startup configuration; with neither source,
// it is a no-op because Remove has no request options and must not guess.
func (k *IstioVirtualService) Remove(ctx context.Context, id router.InstanceID) error {
	appNamespace, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	virtualService, err := cli.VirtualServices(appNamespace).Get(ctx, k.vsName(id), metav1.GetOptions{})
	if err != nil {
		return err
	}
	gatewayRef := virtualService.Annotations[gatewayReferenceAnnotation]
	if gatewayRef == "" && k.GatewayName != "" {
		gatewayNamespace := k.GatewayNamespace
		if gatewayNamespace == "" {
			gatewayNamespace = appNamespace
		}
		gatewayRef = k.gatewayReference(k.GatewayName, gatewayNamespace, appNamespace)
	}
	if gatewayRef == "" {
		return nil
	}
	virtualService.Spec.Gateways = removeFromSet(virtualService.Spec.Gateways, gatewayRef)
	delete(virtualService.Annotations, gatewayReferenceAnnotation)
	_, err = cli.VirtualServices(appNamespace).Update(ctx, virtualService, metav1.UpdateOptions{})
	return err
}
