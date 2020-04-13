// Copyright 2018 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/config/kube/crd"
	"istio.io/istio/pilot/pkg/model"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	placeHolderServiceName = "kubernetes-router-placeholder"

	hostsAnnotation = "tsuru.io/additional-hosts"
)

// IstioGateway manages gateways in a Kubernetes cluster with istio enabled.
type IstioGateway struct {
	*BaseService
	istioClient     model.ConfigStore
	DefaultDomain   string
	GatewaySelector map[string]string
}

func gatewayName(appName string) string {
	return appName
}

func vsName(appName string) string {
	return appName
}

func (k *IstioGateway) gatewayHost(appName string) string {
	return fmt.Sprintf("%v.%v", appName, k.DefaultDomain)
}

func makeConfig(name, ns string, schema model.ProtoSchema) *model.Config {
	config := &model.Config{
		ConfigMeta: model.ConfigMeta{
			Name:      name,
			Namespace: ns,
			Type:      schema.Type,
			Version:   schema.Version,
			Group:     crd.ResourceGroup(&schema),
		},
	}
	return config
}

func (k *IstioGateway) setConfigMeta(config *model.Config, appName string, routerOpts router.Opts) {
	if config.ConfigMeta.Labels == nil {
		config.ConfigMeta.Labels = make(map[string]string)
	}
	if config.ConfigMeta.Annotations == nil {
		config.ConfigMeta.Annotations = make(map[string]string)
	}
	for k, v := range k.Labels {
		config.ConfigMeta.Labels[k] = v
	}
	config.ConfigMeta.Labels[appLabel] = appName
	for k, v := range k.Annotations {
		config.ConfigMeta.Annotations[k] = v
	}
	for k, v := range routerOpts.AdditionalOpts {
		if !strings.Contains(k, "/") {
			config.ConfigMeta.Annotations[annotationWithPrefix(k)] = v
		} else {
			config.ConfigMeta.Annotations[k] = v
		}
	}
}

func (k *IstioGateway) getClient() (model.ConfigStore, error) {
	if k.istioClient != nil {
		return k.istioClient, nil
	}
	var err error
	k.istioClient, err = crd.NewClient("", "", model.IstioConfigTypes, "")
	if err != nil {
		return nil, err
	}
	return k.istioClient, nil
}

func (k *IstioGateway) getVS(cli model.ConfigStore, appName string) (*model.Config, *networking.VirtualService, error) {
	ns, err := k.getAppNamespace(appName)
	if err != nil {
		return nil, nil, err
	}
	vsConfig, found := cli.Get(model.VirtualService.Type, vsName(appName), ns)
	if !found {
		return nil, nil, fmt.Errorf("virtualservice %q not found", vsName(appName))
	}
	vsSpec, ok := vsConfig.Spec.(*networking.VirtualService)
	if !ok {
		return nil, nil, fmt.Errorf("virtualservice does not match type: %T - %#v", vsConfig.Spec, vsConfig.Spec)
	}
	return vsConfig, vsSpec, nil
}

func (k *IstioGateway) isSwapped(obj *model.Config) (string, bool) {
	target := obj.Labels[swapLabel]
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

func hostsFromAnnotation(virtualSvcCfg *model.Config) []string {
	hostsRaw := virtualSvcCfg.Annotations[hostsAnnotation]
	var hosts []string
	if hostsRaw != "" {
		hosts = strings.Split(hostsRaw, ",")
	}
	return hosts
}

func vsAddHost(virtualSvcCfg *model.Config, vsSpec *networking.VirtualService, host string) {
	hosts := hostsFromAnnotation(virtualSvcCfg)
	vsSpec.Hosts = removeFromSet(vsSpec.Hosts, hosts...)
	hosts = addToSet(hosts, host)
	vsSpec.Hosts = addToSet(vsSpec.Hosts, hosts...)
	sort.Strings(hosts)
	virtualSvcCfg.Annotations[hostsAnnotation] = strings.Join(hosts, ",")
	virtualSvcCfg.Spec = vsSpec
}

func vsRemoveHost(virtualSvcCfg *model.Config, vsSpec *networking.VirtualService, host string) {
	hosts := hostsFromAnnotation(virtualSvcCfg)
	vsSpec.Hosts = removeFromSet(vsSpec.Hosts, hosts...)
	hosts = removeFromSet(hosts, host)
	vsSpec.Hosts = addToSet(vsSpec.Hosts, hosts...)
	sort.Strings(hosts)
	virtualSvcCfg.Annotations[hostsAnnotation] = strings.Join(hosts, ",")
	virtualSvcCfg.Spec = vsSpec
}

func (k *IstioGateway) updateVirtualService(virtualSvcCfg *model.Config, vsSpec *networking.VirtualService, appName, dstHost string) *model.Config {
	vsSpec.Gateways = addToSet(vsSpec.Gateways, gatewayName(appName))
	vsSpec.Hosts = addToSet(vsSpec.Hosts, k.gatewayHost(appName))
	if dstHost != placeHolderServiceName {
		vsSpec.Hosts = addToSet(vsSpec.Hosts, dstHost)
	}
	if len(vsSpec.Http) == 0 {
		vsSpec.Http = append(vsSpec.Http, &networking.HTTPRoute{})
	}
	dstIdx := -1
	for i, dst := range vsSpec.Http[0].Route {
		if dst.Destination != nil &&
			(dst.Destination.Host == dstHost || dst.Destination.Host == placeHolderServiceName) {
			dstIdx = i
			break
		}
	}
	if dstIdx == -1 {
		vsSpec.Http[0].Route = append(vsSpec.Http[0].Route, &networking.DestinationWeight{})
		dstIdx = len(vsSpec.Http[0].Route) - 1
	}
	vsSpec.Http[0].Route[dstIdx].Destination = &networking.Destination{
		Host: dstHost,
	}
	virtualSvcCfg.Spec = vsSpec
	return virtualSvcCfg
}

// Create adds a new gateway and a virtualservice for the app
func (k *IstioGateway) Create(appName string, routerOpts router.Opts) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	namespace, err := k.getAppNamespace(appName)
	if err != nil {
		return err
	}
	gatewayCfg := makeConfig(gatewayName(appName), namespace, model.Gateway)
	k.setConfigMeta(gatewayCfg, appName, routerOpts)
	gatewayCfg.Spec = &networking.Gateway{
		Selector: k.GatewaySelector,
		Servers: []*networking.Server{
			{
				Port: &networking.Port{
					Number:   80,
					Name:     "http2",
					Protocol: "HTTP2",
				},
				Hosts: []string{"*"},
			},
		},
	}
	_, err = cli.Create(*gatewayCfg)
	isAlreadyExists := false
	if k8sErrors.IsAlreadyExists(err) {
		isAlreadyExists = true
	} else if err != nil {
		return err
	}

	existingSvc := true
	virtualSvcCfg, vsSpec, err := k.getVS(cli, appName)
	if err != nil {
		existingSvc = false
		virtualSvcCfg = makeConfig(vsName(appName), namespace, model.VirtualService)
		vsSpec = &networking.VirtualService{
			Gateways: []string{"mesh"},
		}
	}
	k.setConfigMeta(virtualSvcCfg, appName, routerOpts)

	webServiceName := placeHolderServiceName
	webService, err := k.getWebService(appName, router.RoutesRequestExtraData{}, virtualSvcCfg.Labels)
	if err == nil {
		webServiceName = webService.Name
	} else {
		log.Printf("ignored error trying to find app web service: %v", err)
	}

	virtualSvcCfg = k.updateVirtualService(virtualSvcCfg, vsSpec, appName, webServiceName)
	if existingSvc {
		_, err = cli.Update(*virtualSvcCfg)
	} else {
		_, err = cli.Create(*virtualSvcCfg)
	}
	if err != nil {
		return err
	}

	if isAlreadyExists {
		return router.ErrIngressAlreadyExists
	}
	return nil
}

// Update sets the app web service into the existing virtualservice
func (k *IstioGateway) Update(appName string, extraData router.RoutesRequestExtraData) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	vsConfig, vsSpec, err := k.getVS(cli, appName)
	if err != nil {
		return err
	}
	service, err := k.getWebService(appName, extraData, vsConfig.Labels)
	if err != nil {
		return err
	}
	if extraData.Namespace != "" && extraData.Service != "" {
		vsConfig.Labels[appBaseServiceNamespaceLabel] = extraData.Namespace
		vsConfig.Labels[appBaseServiceNameLabel] = extraData.Service
	}
	vsConfig = k.updateVirtualService(vsConfig, vsSpec, appName, service.Name)
	k.setConfigMeta(vsConfig, appName, router.Opts{})
	_, err = cli.Update(*vsConfig)
	return err
}

// Get returns the address in the gateway
func (k *IstioGateway) GetAddresses(appName string) ([]string, error) {
	return []string{k.gatewayHost(appName)}, nil
}

// Swap is not implemented
func (k *IstioGateway) Swap(srcApp, dstApp string) error {
	return errors.New("swap is not supported, the virtualservice should be edited manually")
}

// Remove removes the application gateway and removes it from the virtualservice
func (k *IstioGateway) Remove(appName string) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	cfg, spec, err := k.getVS(cli, appName)
	if err != nil {
		return err
	}
	if dstApp, swapped := k.isSwapped(cfg); swapped {
		return ErrAppSwapped{App: appName, DstApp: dstApp}
	}
	ns, err := k.getAppNamespace(appName)
	if err != nil {
		return err
	}
	var gateways []string
	for _, g := range spec.Gateways {
		if g != gatewayName(appName) {
			gateways = append(gateways, g)
		}
	}
	spec.Gateways = gateways
	cfg.Spec = spec
	_, err = cli.Update(*cfg)
	if err != nil {
		return err
	}
	return cli.Delete(model.Gateway.Type, gatewayName(appName), ns)
}

// SetCname adds a new host to the gateway
func (k *IstioGateway) SetCname(appName string, cname string) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	cfg, vsSpec, err := k.getVS(cli, appName)
	if err != nil {
		return err
	}
	vsAddHost(cfg, vsSpec, cname)
	_, err = cli.Update(*cfg)
	return err
}

// GetCnames returns hosts in gateway
func (k *IstioGateway) GetCnames(appName string) (*router.CnamesResp, error) {
	cli, err := k.getClient()
	if err != nil {
		return nil, err
	}
	vsConfig, _, err := k.getVS(cli, appName)
	if err != nil {
		return nil, err
	}
	var rsp router.CnamesResp
	hostsRaw := vsConfig.Annotations[hostsAnnotation]
	for _, h := range strings.Split(hostsRaw, ",") {
		if h != "" {
			rsp.Cnames = append(rsp.Cnames, h)
		}
	}
	return &rsp, nil
}

// UnsetCname removes a host from a gateway
func (k *IstioGateway) UnsetCname(appName string, cname string) error {
	cli, err := k.getClient()
	if err != nil {
		return err
	}
	cfg, vsSpec, err := k.getVS(cli, appName)
	if err != nil {
		return err
	}
	vsRemoveHost(cfg, vsSpec, cname)
	_, err = cli.Update(*cfg)
	return err
}
