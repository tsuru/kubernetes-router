// Copyright 2024 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/opentracing/opentracing-go"
	"github.com/tsuru/kubernetes-router/router"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	labelIsGatewayHTTPs   = "router.tsuru.io/is-https"
	labelCNameHTTPRoute   = "router.tsuru.io/is-cname"
	labelCertIssuer       = "router.tsuru.io/cert-issuer"
	annotationCNames      = "router.tsuru.io/cnames"
	annotationCertIssuers = "router.tsuru.io/cert-issuers"
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
	notCName, _ := labels.NewRequirement(labelCNameHTTPRoute, selection.DoesNotExist, nil)
	selector := labels.Set{appLabel: id.AppName, routerInstanceLabel: id.InstanceName}.AsSelector().Add(*notCName)
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

	ns, err := g.getAppNamespace(ctx, id.AppName)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	if o.Opts.GatewayName != "" {
		g.GatewayName = o.Opts.GatewayName
	}
	if o.Opts.GatewayNamespace != "" {
		g.GatewayNamespace = o.Opts.GatewayNamespace
	} else {
		g.GatewayNamespace = ns
	}

	if g.GatewayName == "" || g.GatewayNamespace == "" {
		err := fmt.Errorf("gateway name and namespace must be specified via startup flags or X-Gateway-Name/X-Gateway-Namespace headers")
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

	if o.Opts.DomainSuffix != "" {
		g.DomainSuffix = o.Opts.DomainSuffix
	}

	rc := httpRouteContext{
		backendTargets:  backendTargets,
		backendServices: backendServices,
	}

	if o.Opts.ExposeAllServices {
		err = g.ensurePerPrefixHTTPRoutes(ctx, span, client, id, o, rc)
	} else {
		err = g.ensureSingleHTTPRoute(ctx, span, client, id, o, rc)
	}
	if err != nil {
		return err
	}

	// Handle CNames: ListenerSets + CName HTTPRoutes
	if len(o.CNames) > 0 || g.hasExistingCNames(ctx, client, id, ns) {
		err = g.ensureCNames(ctx, span, client, id, o, ns, backendTargets["default"])
		if err != nil {
			setSpanError(span, err)
			return err
		}
		// Store CNames in annotation on the main HTTPRoute for tracking
		g.updateCNamesAnnotation(ctx, client, id, ns, o.CNames)
	}

	return nil
}

// httpRouteContext holds the resolved configuration for creating/updating HTTPRoutes.
type httpRouteContext struct {
	ns              string
	backendTargets  map[string]router.BackendTarget
	backendServices map[string]*corev1.Service
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
	rc httpRouteContext,
) error {
	routeName := g.httpRouteName(id)

	isNew := false
	existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(rc.ns).Get(ctx, routeName, metav1.GetOptions{})
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

	for prefixString, svc := range rc.backendServices {
		host := g.buildHTTPRouteHostname(prefixString, id, o, g.DomainSuffix)
		hostnames = append(hostnames, gatewayv1.Hostname(host))
		rules = append(rules, g.buildHTTPRouteRule(svc))
	}

	gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      g.httpRouteName(id),
			Namespace: rc.ns,
			Labels: map[string]string{
				appLabel:                     id.AppName,
				teamLabel:                    o.Team,
				routerInstanceLabel:          id.InstanceName,
				appBaseServiceNamespaceLabel: rc.backendTargets["default"].Namespace,
				appBaseServiceNameLabel:      rc.backendTargets["default"].Service,
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:      gatewayv1.ObjectName(g.GatewayName),
						Namespace: &gwNamespace,
					},
				},
			},
			Hostnames: hostnames,
			Rules:     rules,
		},
	}

	if isNew {
		_, err = client.GatewayV1().HTTPRoutes(rc.ns).Create(ctx, httpRoute, metav1.CreateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
	} else {
		httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
		// Preserve existing annotations (e.g. CNames tracking)
		if existingHTTPRoute.Annotations != nil {
			httpRoute.Annotations = existingHTTPRoute.Annotations
		}
		_, err = client.GatewayV1().HTTPRoutes(rc.ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
	}

	// Clean up any per-prefix HTTPRoutes left over from a previous all-prefixes=true run
	existingRoutes, err := g.listHTTPRoutesForApp(ctx, client, rc.ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	for _, route := range existingRoutes {
		if route.Name != routeName && route.Annotations[AnnotationFreeze] != "true" && route.Labels[labelCNameHTTPRoute] != "true" {
			err = client.GatewayV1().HTTPRoutes(rc.ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
			if err != nil && !k8sErrors.IsNotFound(err) {
				setSpanError(span, err)
				return err
			}

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
	rc httpRouteContext,
) error {
	desiredRouteNames := map[string]bool{}

	// Sort prefixes for deterministic ordering
	prefixes := make([]string, 0, len(rc.backendServices))
	for prefixString := range rc.backendServices {
		prefixes = append(prefixes, prefixString)
	}
	sort.Strings(prefixes)

	for _, prefixString := range prefixes {
		svc := rc.backendServices[prefixString]
		target := rc.backendTargets[prefixString]

		routeName := g.httpRouteNameForPrefix(id, prefixString)
		desiredRouteNames[routeName] = true

		isNew := false
		existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(rc.ns).Get(ctx, routeName, metav1.GetOptions{})
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

		host := g.buildHTTPRouteHostname(prefixString, id, o, g.DomainSuffix)

		gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)
		httpRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      routeName,
				Namespace: rc.ns,
				Labels: map[string]string{
					appLabel:                     id.AppName,
					teamLabel:                    o.Team,
					routerInstanceLabel:          id.InstanceName,
					appBaseServiceNamespaceLabel: target.Namespace,
					appBaseServiceNameLabel:      target.Service,
				},
			},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{
							Name:      gatewayv1.ObjectName(g.GatewayName),
							Namespace: &gwNamespace,
						},
					},
				},
				Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(host)},
				Rules:     []gatewayv1.HTTPRouteRule{g.buildHTTPRouteRule(svc)},
			},
		}

		if isNew {
			_, err = client.GatewayV1().HTTPRoutes(rc.ns).Create(ctx, httpRoute, metav1.CreateOptions{})
		} else {
			httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
			if existingHTTPRoute.Annotations != nil {
				httpRoute.Annotations = existingHTTPRoute.Annotations
			}
			_, err = client.GatewayV1().HTTPRoutes(rc.ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
		}

		if err != nil {
			setSpanError(span, err)
			return err
		}

	}

	// Clean up HTTPRoutes that are no longer needed (e.g., removed prefixes)
	existingRoutes, err := g.listHTTPRoutesForApp(ctx, client, rc.ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}
	for _, route := range existingRoutes {
		if !desiredRouteNames[route.Name] && route.Annotations[AnnotationFreeze] != "true" && route.Labels[labelCNameHTTPRoute] != "true" {
			err = client.GatewayV1().HTTPRoutes(rc.ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
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

	// Remove CName resources (ListenerSets and CName HTTPRoutes)
	err = g.removeCNameResources(ctx, client, id, ns)
	if err != nil {
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

	gw, err := client.GatewayV1().Gateways(g.GatewayNamespace).Get(ctx, g.GatewayName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	schema := "http"
	if gw.Labels[labelIsGatewayHTTPs] == "true" {
		schema = "https"
	}

	var addresses []string
	for _, route := range routes {
		for _, hostname := range route.Spec.Hostnames {
			addresses = append(addresses, fmt.Sprintf("%s://%s", schema, hostname))
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
			addresses = append(addresses, fmt.Sprintf("%s://%s", schema, hostname))
		}
		//TODO: handle https addresses
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

// listenerSetName generates a deterministic name for a ListenerSet based on app + issuer.
func (g *GatewayAPIService) listenerSetName(id router.InstanceID, issuer string) string {
	base := fmt.Sprintf("kubernetes-router-%s-%s-ls", id.AppName, issuer)
	return g.hashedResourceName(id, base, 253)
}

// httpRouteCNameName generates a deterministic name for an HTTPRoute for a CName.
func (g *GatewayAPIService) httpRouteCNameName(id router.InstanceID, cname string) string {
	base := fmt.Sprintf("kubernetes-router-%s-%s-cname", id.AppName, cname)
	return g.hashedResourceName(id, base, 253)
}

// listenerEntryName generates a listener entry name from a cname (sanitized for k8s).
func listenerEntryName(cname string) gatewayv1.SectionName {
	name := strings.ReplaceAll(cname, ".", "-")
	name = strings.ReplaceAll(name, "*", "wildcard")
	if len(name) > 63 {
		name = name[:63]
	}
	return gatewayv1.SectionName(name)
}

// ensureCNames handles CName creation/removal for the GatewayAPI workflow.
// It creates ListenerSets (one per app+issuer) and CName HTTPRoutes.
// When HTTP-only mode is enabled, CName HTTPRoutes connect directly to the Gateway
// without creating ListenerSets or TLS configuration.
func (g *GatewayAPIService) ensureCNames(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	ns string,
	defaultTarget router.BackendTarget,
) error {
	// Determine existing CNames from annotation on the main HTTPRoute
	existingCNames := g.getExistingCNames(ctx, client, id, ns)
	_, cnamesToRemove := diffCNames(existingCNames, o.CNames)

	gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)
	if o.Opts.HTTPOnly {
		// HTTP-only: CName HTTPRoutes connect directly to the Gateway (no TLS/ListenerSets).
		parentRefs := []gatewayv1.ParentReference{
			{
				Name:      gatewayv1.ObjectName(g.GatewayName),
				Namespace: &gwNamespace,
			},
		}
		for _, cname := range o.CNames {
			err := g.ensureCNameHTTPRoute(ctx, span, client, ensureCNameHTTPRouteOpts{
				id:            id,
				ns:            ns,
				cname:         cname,
				team:          o.Team,
				defaultTarget: defaultTarget,
				parentRefs:    parentRefs,
			})
			if err != nil {
				return err
			}
		}

		for _, cname := range cnamesToRemove {
			if err := g.removeCNameHTTPRoute(ctx, client, id, ns, cname); err != nil {
				return err
			}
		}

		// Clean up any ListenerSets that may exist from a previous non-HTTP-only run
		return g.cleanupOrphanedListenerSets(ctx, client, id, ns, nil, nil)
	}

	// Group CNames by issuer
	cnamesByIssuer := map[string][]string{}
	for _, cname := range o.CNames {
		issuer := o.CertIssuers[cname]
		if issuer == "" {
			issuer = g.AcmeIssuer
		}
		if issuer != "" {
			cnamesByIssuer[issuer] = append(cnamesByIssuer[issuer], cname)
		}
	}

	// Ensure ListenerSets and CName HTTPRoutes
	for issuer, cnames := range cnamesByIssuer {
		err := g.ensureListenerSet(ctx, span, client, id, o, ns, issuer, cnames)
		if err != nil {
			return err
		}

		for _, cname := range cnames {
			lsNamespace := gatewayv1.Namespace(ns)
			lsGroup := gatewayv1.Group("gateway.networking.k8s.io")
			lsKind := gatewayv1.Kind("ListenerSet")
			sectionName := listenerEntryName(cname)
			parentRefs := []gatewayv1.ParentReference{
				{
					Group:       &lsGroup,
					Kind:        &lsKind,
					Name:        gatewayv1.ObjectName(g.listenerSetName(id, issuer)),
					Namespace:   &lsNamespace,
					SectionName: &sectionName,
				},
			}

			err := g.ensureCNameHTTPRoute(ctx, span, client, ensureCNameHTTPRouteOpts{
				id:            id,
				ns:            ns,
				cname:         cname,
				team:          o.Team,
				defaultTarget: defaultTarget,
				parentRefs:    parentRefs,
			})
			if err != nil {
				return err
			}
		}
	}

	// Remove CNames that are no longer desired
	for _, cname := range cnamesToRemove {
		err := g.removeCNameHTTPRoute(ctx, client, id, ns, cname)
		if err != nil {
			return err
		}
	}

	// Clean up ListenerSets that have no listeners left
	err := g.cleanupOrphanedListenerSets(ctx, client, id, ns, o.CNames, o.CertIssuers)
	if err != nil {
		return err
	}

	return nil
}

// ensureListenerSet creates or updates a ListenerSet for a given app+issuer combination.
func (g *GatewayAPIService) ensureListenerSet(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	ns, issuer string,
	cnames []string,
) error {
	lsName := g.listenerSetName(id, issuer)
	gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)

	// Build listeners from cnames
	var listeners []gatewayv1.ListenerEntry
	for _, cname := range cnames {
		hostname := gatewayv1.Hostname(cname)
		port := gatewayv1.PortNumber(443)
		tlsMode := gatewayv1.TLSModeTerminate
		secretName := g.tlsSecretName(id, cname)

		listener := gatewayv1.ListenerEntry{
			Name:     listenerEntryName(cname),
			Hostname: &hostname,
			Port:     port,
			Protocol: gatewayv1.HTTPSProtocolType,
			TLS: &gatewayv1.ListenerTLSConfig{
				Mode: &tlsMode,
				CertificateRefs: []gatewayv1.SecretObjectReference{
					{
						Name: gatewayv1.ObjectName(secretName),
					},
				},
			},
		}
		listeners = append(listeners, listener)
	}

	listenerSet := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      lsName,
			Namespace: ns,
			Labels: map[string]string{
				appLabel:            id.AppName,
				routerInstanceLabel: id.InstanceName,
				labelCertIssuer:     issuer,
			},
			Annotations: g.listenerSetCertManagerAnnotations(issuer),
		},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(g.GatewayName),
				Namespace: &gwNamespace,
			},
			Listeners: listeners,
		},
	}

	existing, err := client.GatewayV1().ListenerSets(ns).Get(ctx, lsName, metav1.GetOptions{})
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
		// Create
		_, err = client.GatewayV1().ListenerSets(ns).Create(ctx, listenerSet, metav1.CreateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
		return nil
	}

	// Update
	listenerSet.ResourceVersion = existing.ResourceVersion
	_, err = client.GatewayV1().ListenerSets(ns).Update(ctx, listenerSet, metav1.UpdateOptions{})
	if err != nil {
		setSpanError(span, err)
		return err
	}
	return nil
}

// ensureCNameHTTPRouteOpts encapsulates options for creating/updating a CName HTTPRoute.
type ensureCNameHTTPRouteOpts struct {
	id            router.InstanceID
	ns            string
	cname         string
	team          string
	defaultTarget router.BackendTarget
	parentRefs    []gatewayv1.ParentReference
}

// ensureCNameHTTPRoute creates or updates an HTTPRoute for a specific CName.
// The caller is responsible for building the appropriate parentRefs based on the routing mode.
func (g *GatewayAPIService) ensureCNameHTTPRoute(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	opts ensureCNameHTTPRouteOpts,
) error {
	routeName := g.httpRouteCNameName(opts.id, opts.cname)

	svc, err := g.getWebService(ctx, opts.id.AppName, opts.defaultTarget)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	port := gatewayv1.PortNumber(defaultServicePort)
	if len(svc.Spec.Ports) > 0 {
		port = gatewayv1.PortNumber(svc.Spec.Ports[0].Port)
	}

	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: opts.ns,
			Labels: map[string]string{
				appLabel:            opts.id.AppName,
				teamLabel:           opts.team,
				routerInstanceLabel: opts.id.InstanceName,
				labelCNameHTTPRoute: "true",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: opts.parentRefs,
			},
			Hostnames: []gatewayv1.Hostname{gatewayv1.Hostname(opts.cname)},
			Rules: []gatewayv1.HTTPRouteRule{
				{
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
				},
			},
		},
	}

	existing, err := client.GatewayV1().HTTPRoutes(opts.ns).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			setSpanError(span, err)
			return err
		}
		_, err = client.GatewayV1().HTTPRoutes(opts.ns).Create(ctx, httpRoute, metav1.CreateOptions{})
		if err != nil {
			setSpanError(span, err)
			return err
		}
		return nil
	}

	if existing.Annotations[AnnotationFreeze] == "true" {
		log.Printf("CName HTTPRoute is frozen, skipping: %s/%s", existing.Namespace, existing.Name)
		return nil
	}

	httpRoute.ResourceVersion = existing.ResourceVersion
	_, err = client.GatewayV1().HTTPRoutes(opts.ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
	if err != nil {
		setSpanError(span, err)
		return err
	}
	return nil
}

// removeCNameHTTPRoute removes the HTTPRoute for a specific CName.
func (g *GatewayAPIService) removeCNameHTTPRoute(ctx context.Context, client gatewayclient.Interface, id router.InstanceID, ns, cname string) error {
	routeName := g.httpRouteCNameName(id, cname)
	err := client.GatewayV1().HTTPRoutes(ns).Delete(ctx, routeName, metav1.DeleteOptions{})
	if err != nil && !k8sErrors.IsNotFound(err) {
		return err
	}
	return nil
}

// cleanupOrphanedListenerSets removes ListenerSets for issuers that no longer have any CNames.
func (g *GatewayAPIService) cleanupOrphanedListenerSets(
	ctx context.Context,
	client gatewayclient.Interface,
	id router.InstanceID,
	ns string,
	currentCNames []string,
	certIssuers map[string]string,
) error {
	selector := labels.Set{
		appLabel:            id.AppName,
		routerInstanceLabel: id.InstanceName,
	}.AsSelector()

	listenerSets, err := client.GatewayV1().ListenerSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return err
	}

	// Build a set of active issuers
	activeIssuers := map[string]bool{}
	for _, cname := range currentCNames {
		issuer := certIssuers[cname]
		if issuer == "" {
			issuer = g.AcmeIssuer
		}
		if issuer == "" {
			issuer = "default"
		}
		activeIssuers[issuer] = true
	}

	for _, ls := range listenerSets.Items {
		issuer := ls.Labels[labelCertIssuer]
		if !activeIssuers[issuer] {
			err = client.GatewayV1().ListenerSets(ns).Delete(ctx, ls.Name, metav1.DeleteOptions{})
			if err != nil && !k8sErrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

// removeCNameResources removes all CName-related resources (ListenerSets and CName HTTPRoutes) for an app.
func (g *GatewayAPIService) removeCNameResources(ctx context.Context, client gatewayclient.Interface, id router.InstanceID, ns string) error {
	cnameSelector := labels.Set{
		appLabel:            id.AppName,
		routerInstanceLabel: id.InstanceName,
		labelCNameHTTPRoute: "true",
	}.AsSelector()

	cnameRoutes, err := client.GatewayV1().HTTPRoutes(ns).List(ctx, metav1.ListOptions{
		LabelSelector: cnameSelector.String(),
	})
	if err != nil {
		return err
	}
	for _, route := range cnameRoutes.Items {
		err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
		if err != nil && !k8sErrors.IsNotFound(err) {
			return err
		}
	}

	lsSelector := labels.Set{
		appLabel:            id.AppName,
		routerInstanceLabel: id.InstanceName,
	}.AsSelector()

	listenerSets, err := client.GatewayV1().ListenerSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: lsSelector.String(),
	})
	if err != nil {
		return err
	}
	for _, ls := range listenerSets.Items {
		err = client.GatewayV1().ListenerSets(ns).Delete(ctx, ls.Name, metav1.DeleteOptions{})
		if err != nil && !k8sErrors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

// getExistingCNames retrieves the current CNames stored in the main HTTPRoute annotation.
func (g *GatewayAPIService) getExistingCNames(ctx context.Context, client gatewayclient.Interface, id router.InstanceID, ns string) []string {
	routeName := g.httpRouteName(id)
	httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	cnames := httpRoute.Annotations[annotationCNames]
	if cnames == "" {
		return nil
	}
	return strings.Split(cnames, ",")
}

// tlsSecretName generates the secret name for a CName TLS certificate.
func (g *GatewayAPIService) tlsSecretName(id router.InstanceID, cname string) string {
	base := fmt.Sprintf("%s-%s-tls", id.AppName, cname)
	name := strings.ReplaceAll(base, ".", "-")
	name = strings.ReplaceAll(name, "*", "wildcard")
	if len(name) > 253 {
		name = name[:253]
	}
	return name
}

// hasExistingCNames checks if the app currently has any CName annotations stored.
func (g *GatewayAPIService) hasExistingCNames(ctx context.Context, client gatewayclient.Interface, id router.InstanceID, ns string) bool {
	existing := g.getExistingCNames(ctx, client, id, ns)
	return len(existing) > 0
}

// updateCNamesAnnotation stores the current CNames in the main HTTPRoute annotation for tracking.
func (g *GatewayAPIService) updateCNamesAnnotation(ctx context.Context, client gatewayclient.Interface, id router.InstanceID, ns string, cnames []string) {
	routeName := g.httpRouteName(id)
	httpRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		return
	}

	if httpRoute.Annotations == nil {
		httpRoute.Annotations = map[string]string{}
	}

	if len(cnames) > 0 {
		httpRoute.Annotations[annotationCNames] = strings.Join(cnames, ",")
	} else {
		delete(httpRoute.Annotations, annotationCNames)
	}

	_, _ = client.GatewayV1().HTTPRoutes(ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
}

// listenerSetCertManagerAnnotations returns the cert-manager annotations for a ListenerSet.
func (g *GatewayAPIService) listenerSetCertManagerAnnotations(issuer string) map[string]string {
	if issuer == "" || issuer == "default" {
		if g.AcmeIssuer != "" {
			return map[string]string{
				certManagerClusterIssuerKey: g.AcmeIssuer,
			}
		}
		return nil
	}

	if strings.Contains(issuer, ".") {
		parts := strings.SplitN(issuer, ".", 3)
		if len(parts) == 3 {
			return map[string]string{
				certManagerIssuerKey:      parts[0],
				certManagerIssuerKindKey:  parts[1],
				certManagerIssuerGroupKey: parts[2],
			}
		}
	}

	return map[string]string{
		certManagerClusterIssuerKey: issuer,
	}
}
