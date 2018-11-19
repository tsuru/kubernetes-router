// Copyright 2018 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/config/kube/crd"
	"istio.io/istio/pilot/pkg/model"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	placeHolderServiceName = "kubernetes-router-placeholder"
)

// IstioGateway manages gateways in a Kubernetes cluster with istio enabled.
type IstioGateway struct {
	*BaseService
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

func (k *IstioGateway) istioClient() (*crd.Client, error) {
	cli, err := crd.NewClient("", "", model.IstioConfigTypes, "")
	if err != nil {
		return nil, err
	}
	return cli, nil
}

func (k *IstioGateway) getVS(cli *crd.Client, appName string) (*model.Config, *networking.VirtualService, error) {
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

func (k *IstioGateway) getGateway(cli *crd.Client, appName string) (*model.Config, *networking.Gateway, error) {
	ns, err := k.getAppNamespace(appName)
	if err != nil {
		return nil, nil, err
	}
	gatewayConfig, found := cli.Get(model.Gateway.Type, gatewayName(appName), ns)
	if !found {
		return nil, nil, fmt.Errorf("gateway %q not found", gatewayName(appName))
	}
	gatewaySpec, ok := gatewayConfig.Spec.(*networking.Gateway)
	if !ok {
		return nil, nil, fmt.Errorf("gateway does not match type: %T - %#v", gatewayConfig.Spec, gatewayConfig.Spec)
	}
	if len(gatewaySpec.Servers) == 0 || len(gatewaySpec.Servers[0].Hosts) == 0 {
		return nil, nil, fmt.Errorf("no servers or hosts in gateway %q: %#v", gatewayConfig.Name, gatewaySpec)
	}
	return gatewayConfig, gatewaySpec, nil
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

func (k *IstioGateway) updateVirtualService(vsSpec *networking.VirtualService, appName, dstHost string) *networking.VirtualService {
	vsSpec.Gateways = addToSet(vsSpec.Gateways, gatewayName(appName))
	vsSpec.Hosts = addToSet(vsSpec.Hosts, k.gatewayHost(appName), dstHost)
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
	return vsSpec
}

// Create adds a new gateway and a virtualservice for the app
func (k *IstioGateway) Create(appName string, routerOpts router.Opts) error {
	cli, err := k.istioClient()
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
					Name:     "http",
					Protocol: "HTTP",
				},
				Hosts: []string{
					k.gatewayHost(appName),
				},
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

	webServiceName := placeHolderServiceName
	webService, err := k.getWebService(appName)
	if err == nil {
		webServiceName = webService.Name
	} else {
		log.Printf("ignored error trying to find app web service: %v", err)
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
	virtualSvcCfg.Spec = k.updateVirtualService(vsSpec, appName, webServiceName)
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
func (k *IstioGateway) Update(appName string, _ router.Opts) error {
	service, err := k.getWebService(appName)
	if err != nil {
		return err
	}
	cli, err := k.istioClient()
	if err != nil {
		return err
	}
	vsConfig, vsSpec, err := k.getVS(cli, appName)
	if err != nil {
		return err
	}
	vsConfig.Spec = k.updateVirtualService(vsSpec, appName, service.Name)
	_, err = cli.Update(*vsConfig)
	return err
}

// Get returns the address in the gateway
func (k *IstioGateway) Get(appName string) (map[string]string, error) {
	cli, err := k.istioClient()
	if err != nil {
		return nil, err
	}
	_, gatewaySpec, err := k.getGateway(cli, appName)
	if err != nil {
		return nil, err
	}
	return map[string]string{"address": gatewaySpec.Servers[0].Hosts[0]}, nil
}

// Swap is not implemented
func (k *IstioGateway) Swap(srcApp, dstApp string) error {
	return errors.New("swap is not implemented yet")
}

// Remove removes the application gateway and removes it from the virtualservice
func (k *IstioGateway) Remove(appName string) error {
	cli, err := k.istioClient()
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

var errCnameExists = errors.New("cname already exists")

// SetCname adds a new host to the gateway
func (k *IstioGateway) SetCname(appName string, cname string) error {
	cli, err := k.istioClient()
	if err != nil {
		return err
	}
	cfg, spec, err := k.getGateway(cli, appName)
	if err != nil {
		return err
	}
	for _, h := range spec.Servers[0].Hosts {
		if h == cname {
			return errCnameExists
		}
	}
	spec.Servers[0].Hosts = append(spec.Servers[0].Hosts, cname)
	cfg.Spec = spec
	_, err = cli.Update(*cfg)
	return err
}

// GetCnames returns hosts in gateway
func (k *IstioGateway) GetCnames(appName string) (*router.CnamesResp, error) {
	cli, err := k.istioClient()
	if err != nil {
		return nil, err
	}
	_, spec, err := k.getGateway(cli, appName)
	if err != nil {
		return nil, err
	}
	var rsp router.CnamesResp
	for _, h := range spec.Servers[0].Hosts {
		if h != k.gatewayHost(appName) {
			rsp.Cnames = append(rsp.Cnames, h)
		}
	}
	return &rsp, nil
}

// UnsetCname removes a host from a gateway
func (k *IstioGateway) UnsetCname(appName string, cname string) error {
	cli, err := k.istioClient()
	if err != nil {
		return err
	}
	cfg, spec, err := k.getGateway(cli, appName)
	if err != nil {
		return err
	}
	var keep []string
	for _, h := range spec.Servers[0].Hosts {
		if h != cname {
			keep = append(keep, h)
		}
	}
	spec.Servers[0].Hosts = keep
	cfg.Spec = spec
	_, err = cli.Update(*cfg)
	return err
}
