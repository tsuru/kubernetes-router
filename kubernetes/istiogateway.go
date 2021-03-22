// Copyright 2018 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	apiNetworking "istio.io/api/networking/v1beta1"
	networking "istio.io/client-go/pkg/apis/networking/v1beta1"
	networkingClientSet "istio.io/client-go/pkg/clientset/versioned/typed/networking/v1beta1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hostsAnnotation = "tsuru.io/additional-hosts"
)

var (
	_ router.Router = &IstioGateway{}
)

// IstioGateway manages gateways in a Kubernetes cluster with istio enabled.
type IstioGateway struct {
	*BaseService
	istioClient     networkingClientSet.NetworkingV1beta1Interface
	DomainSuffix    string
	GatewaySelector map[string]string
}

func (k *IstioGateway) gatewayName(id router.InstanceID) string {
	return k.hashedResourceName(id, id.AppName, 63)
}

func (k *IstioGateway) vsName(id router.InstanceID) string {
	return k.hashedResourceName(id, id.AppName, 63)
}

func (k *IstioGateway) gatewayHost(id router.InstanceID) string {
	if id.InstanceName == "" {
		return fmt.Sprintf("%v.%v", id.AppName, k.DomainSuffix)
	}
	return fmt.Sprintf("%v.instance.%v.%v", id.InstanceName, id.AppName, k.DomainSuffix)
}

func (k *IstioGateway) updateObjectMeta(result *metav1.ObjectMeta, appName string, routerOpts router.Opts) {
	if result.Labels == nil {
		result.Labels = make(map[string]string)
	}
	if result.Annotations == nil {
		result.Annotations = make(map[string]string)
	}
	for k, v := range k.Labels {
		result.Labels[k] = v
	}
	result.Labels[appLabel] = appName
	for k, v := range k.Annotations {
		result.Annotations[k] = v
	}
	for k, v := range routerOpts.AdditionalOpts {
		result.Annotations[k] = v
	}
}

func (k *IstioGateway) getClient() (networkingClientSet.NetworkingV1beta1Interface, error) {
	if k.istioClient != nil {
		return k.istioClient, nil
	}
	var err error

	restConfig, err := k.getConfig()
	if err != nil {
		return nil, err
	}

	k.istioClient, err = networkingClientSet.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	return k.istioClient, nil
}

func (k *IstioGateway) getVS(ctx context.Context, cli networkingClientSet.NetworkingV1beta1Interface, id router.InstanceID) (*networking.VirtualService, error) {
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	return cli.VirtualServices(ns).Get(ctx, k.vsName(id), metav1.GetOptions{})
}

func (k *IstioGateway) isSwapped(meta metav1.ObjectMeta) (target string, isSwapped bool) {
	target = meta.Labels[swapLabel]
	return target, target != ""
}

func addToSet(dst []string, toAdd ...string) []string {
	existingSet := map[string]struct{}{}
	for _, v := range dst {
		existingSet[v] = struct{}{}
	}
	for _, v := range toAdd {
		if _, in := existingSet[v]; !in {
			dst = append(dst, v)
		}
	}
	return dst
}

func removeFromSet(dst []string, toRemove ...string) []string {
	existingSet := map[string]struct{}{}
	for _, v := range dst {
		existingSet[v] = struct{}{}
	}
	for _, v := range toRemove {
		delete(existingSet, v)
	}
	dst = dst[:0]
	for h := range existingSet {
		dst = append(dst, h)
	}
	return dst
}

func hostsFromAnnotation(annotations map[string]string) []string {
	hostsRaw := annotations[hostsAnnotation]
	var hosts []string
	if hostsRaw != "" {
		hosts = strings.Split(hostsRaw, ",")
	}
	return hosts
}

func vsAddHost(v *networking.VirtualService, host string) {
	hosts := hostsFromAnnotation(v.Annotations)
	v.Spec.Hosts = removeFromSet(v.Spec.Hosts, hosts...)
	hosts = addToSet(hosts, host)
	v.Spec.Hosts = addToSet(v.Spec.Hosts, hosts...)
	sort.Strings(hosts)
	v.Annotations[hostsAnnotation] = strings.Join(hosts, ",")
}

func vsRemoveHost(v *networking.VirtualService, host string) {
	hosts := hostsFromAnnotation(v.Annotations)
	v.Spec.Hosts = removeFromSet(v.Spec.Hosts, hosts...)
	hosts = removeFromSet(hosts, host)
	v.Spec.Hosts = addToSet(v.Spec.Hosts, hosts...)
	sort.Strings(hosts)
	v.Annotations[hostsAnnotation] = strings.Join(hosts, ",")
}

func (k *IstioGateway) updateVirtualService(v *networking.VirtualService, id router.InstanceID, dstHost string) {
	v.Spec.Gateways = addToSet(v.Spec.Gateways, k.gatewayName(id))
	v.Spec.Hosts = addToSet(v.Spec.Hosts, k.gatewayHost(id))
	v.Spec.Hosts = addToSet(v.Spec.Hosts, dstHost)

	if len(v.Spec.Http) == 0 {
		v.Spec.Http = append(v.Spec.Http, &apiNetworking.HTTPRoute{})
	}
	dstIdx := -1
	for i, dst := range v.Spec.Http[0].Route {
		if dst.Destination != nil &&
			(dst.Destination.Host == dstHost) {
			dstIdx = i
			break
		}
	}
	if dstIdx == -1 {
		v.Spec.Http[0].Route = append(v.Spec.Http[0].Route, &apiNetworking.HTTPRouteDestination{})
		dstIdx = len(v.Spec.Http[0].Route) - 1
	}
	v.Spec.Http[0].Route[dstIdx].Destination = &apiNetworking.Destination{
		Host: dstHost,
	}
}

// Create adds a new gateway and a virtualservice for the app
func (k *IstioGateway) Ensure(ctx context.Context, id router.InstanceID, o router.EnsureBackendOpts) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	namespace, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}

	defaultTarget, err := k.getDefaultBackendTarget(o.Prefixes)
	if err != nil {
		return err
	}

	gateway := &networking.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name: id.AppName,
		},
		Spec: apiNetworking.Gateway{
			Servers: []*apiNetworking.Server{
				{
					Port: &apiNetworking.Port{
						Number:   80,
						Name:     "http2",
						Protocol: "HTTP2",
					},
					Hosts: []string{"*"},
				},
			},
			Selector: k.GatewaySelector,
		},
	}

	k.updateObjectMeta(&gateway.ObjectMeta, id.AppName, o.Opts)

	_, err = cli.Gateways(namespace).Create(ctx, gateway, metav1.CreateOptions{})
	isAlreadyExists := false
	if k8sErrors.IsAlreadyExists(err) {
		isAlreadyExists = true
	} else if err != nil {
		return err
	}

	existingSvc := true
	virtualSvc, err := k.getVS(ctx, cli, id)

	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}

	if k8sErrors.IsNotFound(err) {
		existingSvc = false
		virtualSvc = &networking.VirtualService{
			ObjectMeta: metav1.ObjectMeta{
				Name: k.vsName(id),
			},
			Spec: apiNetworking.VirtualService{
				Gateways: []string{"mesh"},
			},
		}
	}

	k.updateObjectMeta(&virtualSvc.ObjectMeta, id.AppName, o.Opts)

	webService, err := k.getWebService(ctx, id.AppName, *defaultTarget)
	if err != nil {
		return err
	}

	k.updateVirtualService(virtualSvc, id, webService.Name)
	virtualSvc.Labels[appBaseServiceNamespaceLabel] = defaultTarget.Namespace
	virtualSvc.Labels[appBaseServiceNameLabel] = defaultTarget.Service

	existingCNames := hostsFromAnnotation(virtualSvc.Annotations)
	cnamesToAdd, cnamesToRemove := diffCNames(existingCNames, o.CNames)
	for _, cname := range cnamesToAdd {
		vsAddHost(virtualSvc, cname)
	}
	for _, cname := range cnamesToRemove {
		vsRemoveHost(virtualSvc, cname)
	}

	if existingSvc {
		_, err = cli.VirtualServices(namespace).Update(ctx, virtualSvc, metav1.UpdateOptions{})
	} else {
		_, err = cli.VirtualServices(namespace).Create(ctx, virtualSvc, metav1.CreateOptions{})
	}
	if err != nil {
		return err
	}

	if isAlreadyExists {
		return router.ErrIngressAlreadyExists
	}
	return nil
}

// Get returns the address in the gateway
func (k *IstioGateway) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	return []string{k.gatewayHost(id)}, nil
}

// Swap is not implemented
func (k *IstioGateway) Swap(ctx context.Context, srcApp, dstApp router.InstanceID) error {
	return errors.New("swap is not supported, the virtualservice should be edited manually")
}

// Remove removes the application gateway and removes it from the virtualservice
func (k *IstioGateway) Remove(ctx context.Context, id router.InstanceID) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	virtualSvc, err := k.getVS(ctx, cli, id)
	if err != nil {
		return err
	}
	if dstApp, swapped := k.isSwapped(virtualSvc.ObjectMeta); swapped {
		return ErrAppSwapped{App: id.AppName, DstApp: dstApp}
	}
	ns, err := k.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}
	var gateways []string
	for _, g := range virtualSvc.Spec.Gateways {
		if g != k.gatewayName(id) {
			gateways = append(gateways, g)
		}
	}
	virtualSvc.Spec.Gateways = gateways
	_, err = cli.VirtualServices(ns).Update(ctx, virtualSvc, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return cli.Gateways(ns).Delete(ctx, k.gatewayName(id), metav1.DeleteOptions{})
}

func diffCNames(existing []string, expected []string) (toAdd []string, toRemove []string) {
	mapExisting := map[string]bool{}
	mapExpected := map[string]bool{}

	for _, e := range existing {
		mapExisting[e] = true
	}

	for _, itemExpected := range expected {
		mapExpected[itemExpected] = true
		if !mapExisting[itemExpected] {
			toAdd = append(toAdd, itemExpected)
		}
	}

	for _, e := range existing {
		if !mapExpected[e] {
			toRemove = append(toRemove, e)
		}
	}

	return toAdd, toRemove
}
