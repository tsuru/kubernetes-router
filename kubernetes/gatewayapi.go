// Copyright 2024 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"fmt"
	"log"

	"github.com/opentracing/opentracing-go"
	"github.com/tsuru/kubernetes-router/router"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

var (
	_ router.Router       = &GatewayAPIService{}
	_ router.RouterStatus = &GatewayAPIService{}
)

// GatewayAPIService manages HTTPRoute resources using the Kubernetes Gateway API.
type GatewayAPIService struct {
	*BaseService
	GatewayName      string
	GatewayNamespace string
	DomainSuffix     string
	AcmeIssuer       string
	GatewayClient    gatewayclient.Interface
}

func (g *GatewayAPIService) getGatewayClient() (gatewayclient.Interface, error) {
	if g.GatewayClient != nil {
		return g.GatewayClient, nil
	}
	config, err := g.getConfig()
	if err != nil {
		return nil, err
	}
	g.GatewayClient, err = gatewayclient.NewForConfig(config)
	return g.GatewayClient, err
}

func (g *GatewayAPIService) httpRouteName(id router.InstanceID) string {
	return g.hashedResourceName(id, "kubernetes-router-"+id.AppName+"-httproute", 253)
}

// Ensure creates or updates an HTTPRoute resource to route traffic to the app's backend service.
func (g *GatewayAPIService) Ensure(ctx context.Context, id router.InstanceID, o router.EnsureBackendOpts) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "ensureHTTPRoute")
	defer span.Finish()

	gatewayName := g.GatewayName
	if o.Opts.GatewayName != "" {
		gatewayName = o.Opts.GatewayName
	}
	gatewayNamespace := g.GatewayNamespace
	if o.Opts.GatewayNamespace != "" {
		gatewayNamespace = o.Opts.GatewayNamespace
	}

	if gatewayName == "" || gatewayNamespace == "" {
		err := fmt.Errorf("gateway name and namespace must be specified via startup flags or X-Gateway-Name/X-Gateway-Namespace headers")
		setSpanError(span, err)
		return err
	}

	ns, err := g.getAppNamespace(ctx, id.AppName)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	client, err := g.getGatewayClient()
	if err != nil {
		setSpanError(span, err)
		return err
	}

	isNew := false
	existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, g.httpRouteName(id), metav1.GetOptions{})
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
		isNew = true
		existingHTTPRoute = nil
	}

	if !isNew && existingHTTPRoute != nil {
		if existingHTTPRoute.Annotations[AnnotationFreeze] == "true" {
			log.Printf("HTTPRoute is frozen, skipping: %s/%s", existingHTTPRoute.Namespace, existingHTTPRoute.Name)
			return nil
		}
	}

	backendTargets, err := g.getBackendTargets(o.Prefixes, o.Opts.ExposeAllServices)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	backendServices := map[string]*corev1.Service{}
	for key, target := range backendTargets {
		backendServices[key], err = g.getWebService(ctx, id.AppName, target)
		if err != nil {
			setSpanError(span, err)
			return err
		}
	}

	domainSuffix := g.DomainSuffix
	if o.Opts.DomainSuffix != "" {
		domainSuffix = o.Opts.DomainSuffix
	}

	var hostnames []gatewayv1.Hostname
	var rules []gatewayv1.HTTPRouteRule

	for prefixString, svc := range backendServices {
		prefix := ""
		if prefixString != "default" {
			prefix = prefixString + "."
		}

		var host string
		if len(o.Opts.Domain) > 0 {
			host = fmt.Sprintf("%s%s", prefix, o.Opts.Domain)
		} else if o.Opts.DomainPrefix == "" {
			host = fmt.Sprintf("%s%s.%s", prefix, id.AppName, domainSuffix)
		} else {
			host = fmt.Sprintf("%s%s.%s.%s", prefix, o.Opts.DomainPrefix, id.AppName, domainSuffix)
		}
		hostnames = append(hostnames, gatewayv1.Hostname(host))

		port := gatewayv1.PortNumber(defaultServicePort)
		if len(svc.Spec.Ports) > 0 {
			port = gatewayv1.PortNumber(svc.Spec.Ports[0].Port)
		}
		rules = append(rules, gatewayv1.HTTPRouteRule{
			BackendRefs: []gatewayv1.HTTPBackendRef{
				{
					BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: gatewayv1.ObjectName(svc.Name),
							Port: &port,
						},
					},
				},
			},
		})
	}

	gwNamespace := gatewayv1.Namespace(gatewayNamespace)
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      g.httpRouteName(id),
			Namespace: ns,
			Labels: map[string]string{
				appLabel:                     id.AppName,
				teamLabel:                    o.Team,
				appBaseServiceNamespaceLabel: backendTargets["default"].Namespace,
				appBaseServiceNameLabel:      backendTargets["default"].Service,
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(gatewayName),
						Namespace: &gwNamespace,
					},
				},
			},
			Hostnames: hostnames,
			Rules:     rules,
		},
	}

	if isNew {
		_, err = client.GatewayV1().HTTPRoutes(ns).Create(ctx, httpRoute, metav1.CreateOptions{})
		if err != nil {
			setSpanError(span, err)
		}
		return err
	}

	httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
	_, err = client.GatewayV1().HTTPRoutes(ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
	if err != nil {
		setSpanError(span, err)
	}
	return err
}

// Remove deletes the HTTPRoute resource for the given app.
func (g *GatewayAPIService) Remove(ctx context.Context, id router.InstanceID) error {
	span, ctx := opentracing.StartSpanFromContext(ctx, "removeHTTPRoute")
	defer span.Finish()

	ns, err := g.getAppNamespace(ctx, id.AppName)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	client, err := g.getGatewayClient()
	if err != nil {
		setSpanError(span, err)
		return err
	}

	err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, g.httpRouteName(id), metav1.DeleteOptions{})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		setSpanError(span, err)
	}
	return err
}

// GetAddresses returns the hostnames configured on the HTTPRoute for the given app.
func (g *GatewayAPIService) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "getGatewayAddresses")
	defer span.Finish()

	ns, err := g.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}

	client, err := g.getGatewayClient()
	if err != nil {
		return nil, err
	}

	httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, g.httpRouteName(id), metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	var addresses []string
	for _, hostname := range httpRoute.Spec.Hostnames {
		addresses = append(addresses, string(hostname))
	}
	return addresses, nil
}

// GetStatus returns the readiness status of the HTTPRoute by inspecting its parent conditions.
func (g *GatewayAPIService) GetStatus(ctx context.Context, id router.InstanceID) (router.BackendStatus, string, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "getHTTPRouteStatus")
	defer span.Finish()

	ns, err := g.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}

	client, err := g.getGatewayClient()
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}

	httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, g.httpRouteName(id), metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return router.BackendStatusNotReady, "waiting for deploy", nil
		}
		return router.BackendStatusNotReady, "", err
	}

	return g.httpRouteStatus(ctx, ns, httpRoute)
}

func (g *GatewayAPIService) httpRouteStatus(ctx context.Context, ns string, httpRoute *gatewayv1.HTTPRoute) (router.BackendStatus, string, error) {
	for _, parent := range httpRoute.Status.Parents {
		for _, cond := range parent.Conditions {
			if cond.Type == "Accepted" {
				if cond.Status == "True" {
					return router.BackendStatusReady, "", nil
				}
				return router.BackendStatusNotReady, cond.Message, nil
			}
		}
	}

	detail, err := g.getStatusForRuntimeObject(ctx, ns, "HTTPRoute", httpRoute.UID)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}
	return router.BackendStatusNotReady, detail, nil
}

// SupportedOptions returns the options supported by this router.
func (g *GatewayAPIService) SupportedOptions(ctx context.Context) map[string]string {
	return router.DescribedOptions()
}
