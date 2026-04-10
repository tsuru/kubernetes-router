// Copyright 2024 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/opentracing/opentracing-go"
	"github.com/tsuru/kubernetes-router/router"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func (g *GatewayAPIService) httpRouteNameForPrefix(id router.InstanceID, prefix string) string {
	if prefix == "default" {
		return g.httpRouteName(id)
	}
	return g.hashedResourceName(id, "kubernetes-router-"+id.AppName+"-"+prefix+"-httproute", 253)
}

// listHTTPRoutesForApp lists all HTTPRoutes labeled for the given app in the namespace.
func (g *GatewayAPIService) listHTTPRoutesForApp(ctx context.Context, client gatewayclient.Interface, ns string, id router.InstanceID) ([]gatewayv1.HTTPRoute, error) {
	selector := labels.Set{appLabel: id.AppName}.AsSelector()
	list, err := client.GatewayV1().HTTPRoutes(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// Ensure creates or updates HTTPRoute resources to route traffic to the app's backend service.
// When ExposeAllServices is true, a separate HTTPRoute is created for each prefix/version.
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

	gwNamespace := gatewayv1.Namespace(gatewayNamespace)

	if o.Opts.ExposeAllServices {
		return g.ensurePerPrefixHTTPRoutes(ctx, span, client, id, o, ns, gatewayName, gwNamespace, domainSuffix, backendTargets, backendServices)
	}

	return g.ensureSingleHTTPRoute(ctx, span, client, id, o, ns, gatewayName, gwNamespace, domainSuffix, backendTargets, backendServices)
}

func (g *GatewayAPIService) buildHTTPRouteHostname(prefixString string, id router.InstanceID, o router.EnsureBackendOpts, domainSuffix string) string {
	prefix := ""
	if prefixString != "default" {
		prefix = prefixString + "."
	}
	if len(o.Opts.Domain) > 0 {
		return fmt.Sprintf("%s%s", prefix, o.Opts.Domain)
	}
	if o.Opts.DomainPrefix == "" {
		return fmt.Sprintf("%s%s.%s", prefix, id.AppName, domainSuffix)
	}
	return fmt.Sprintf("%s%s.%s.%s", prefix, o.Opts.DomainPrefix, id.AppName, domainSuffix)
}

func (g *GatewayAPIService) buildHTTPRouteRule(svc *corev1.Service) gatewayv1.HTTPRouteRule {
	port := gatewayv1.PortNumber(defaultServicePort)
	if len(svc.Spec.Ports) > 0 {
		port = gatewayv1.PortNumber(svc.Spec.Ports[0].Port)
	}
	return gatewayv1.HTTPRouteRule{
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
	}
}

func (g *GatewayAPIService) ensureSingleHTTPRoute(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	ns, gatewayName string,
	gwNamespace gatewayv1.Namespace,
	domainSuffix string,
	backendTargets map[string]router.BackendTarget,
	backendServices map[string]*corev1.Service,
) error {
	routeName := g.httpRouteName(id)

	isNew := false
	existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, routeName, metav1.GetOptions{})
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

	var hostnames []gatewayv1.Hostname
	var rules []gatewayv1.HTTPRouteRule

	for prefixString, svc := range backendServices {
		host := g.buildHTTPRouteHostname(prefixString, id, o, domainSuffix)
		hostnames = append(hostnames, gatewayv1.Hostname(host))
		rules = append(rules, g.buildHTTPRouteRule(svc))
	}

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
			return err
		}
	} else {
		httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
		_, err = client.GatewayV1().HTTPRoutes(ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
	}

	// Clean up any per-prefix HTTPRoutes left over from a previous all-prefixes=true run
	existingRoutes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	for _, route := range existingRoutes {
		if route.Name == routeName {
			continue
		}
		if route.Annotations[AnnotationFreeze] == "true" {
			continue
		}
		err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
		if err != nil && !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
	}

	return nil
}

func (g *GatewayAPIService) ensurePerPrefixHTTPRoutes(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	ns, gatewayName string,
	gwNamespace gatewayv1.Namespace,
	domainSuffix string,
	backendTargets map[string]router.BackendTarget,
	backendServices map[string]*corev1.Service,
) error {
	desiredRouteNames := map[string]bool{}

	// Sort prefixes for deterministic ordering
	prefixes := make([]string, 0, len(backendServices))
	for prefixString := range backendServices {
		prefixes = append(prefixes, prefixString)
	}
	sort.Strings(prefixes)

	for _, prefixString := range prefixes {
		svc := backendServices[prefixString]
		target := backendTargets[prefixString]

		routeName := g.httpRouteNameForPrefix(id, prefixString)
		desiredRouteNames[routeName] = true

		isNew := false
		existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, routeName, metav1.GetOptions{})
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
				continue
			}
		}

		host := g.buildHTTPRouteHostname(prefixString, id, o, domainSuffix)

		httpRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routeName,
				Namespace: ns,
				Labels: map[string]string{
					appLabel:                     id.AppName,
					teamLabel:                    o.Team,
					appBaseServiceNamespaceLabel: target.Namespace,
					appBaseServiceNameLabel:      target.Service,
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
				Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(host)},
				Rules:     []gatewayv1.HTTPRouteRule{g.buildHTTPRouteRule(svc)},
			},
		}

		if isNew {
			_, err = client.GatewayV1().HTTPRoutes(ns).Create(ctx, httpRoute, metav1.CreateOptions{})
		} else {
			httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
			_, err = client.GatewayV1().HTTPRoutes(ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
		}

		if err != nil {
			setSpanError(span, err)
			return err
		}

	}

	// Clean up HTTPRoutes that are no longer needed (e.g., removed prefixes)
	existingRoutes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	for _, route := range existingRoutes {
		if !desiredRouteNames[route.Name] && route.Annotations[AnnotationFreeze] != "true" {
			err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
			if err != nil && !k8sErrors.IsNotFound(err) {
				setSpanError(span, err)
				return err
			}
		}
	}

	return nil
}

// Remove deletes all HTTPRoute resources for the given app (both single and per-prefix).
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

	routes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	for _, route := range routes {
		err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
		if err != nil && !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
	}

	// Also try deleting the single-mode route by name in case it wasn't labeled
	err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, g.httpRouteName(id), metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		setSpanError(span, err)
		return err
	}

	return nil
}

// GetAddresses returns the hostnames configured on all HTTPRoutes for the given app.
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

	routes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		return nil, err
	}

	var addresses []string
	for _, route := range routes {
		for _, hostname := range route.Spec.Hostnames {
			addresses = append(addresses, string(hostname))
		}
	}

	if len(addresses) == 0 {
		// Fallback: try fetching by the single-mode name
		httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, g.httpRouteName(id), metav1.GetOptions{})
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		for _, hostname := range httpRoute.Spec.Hostnames {
			addresses = append(addresses, string(hostname))
		}
	}

	return addresses, nil
}

// GetStatus returns the readiness status of the HTTPRoutes by inspecting their parent conditions.
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

	routes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}

	if len(routes) == 0 {
		// Fallback: try fetching by single-mode name
		httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, g.httpRouteName(id), metav1.GetOptions{})
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return router.BackendStatusNotReady, "waiting for deploy", nil
			}
			return router.BackendStatusNotReady, "", err
		}
		return g.httpRouteStatus(ctx, ns, httpRoute)
	}

	for i := range routes {
		status, detail, err := g.httpRouteStatus(ctx, ns, &routes[i])
		if err != nil {
			return router.BackendStatusNotReady, "", err
		}
		if status != router.BackendStatusReady {
			return status, detail, nil
		}
	}

	return router.BackendStatusReady, "", nil
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
