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
	"k8s.io/apimachinery/pkg/util/validation"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned"
)

const (
	labelHTTPRouteHTTPOnly = "router.tsuru.io/http-only"
	labelCNameHTTPRoute    = "router.tsuru.io/is-cname"
	labelCertIssuer        = "router.tsuru.io/cert-issuer"
	annotationCNames       = "router.tsuru.io/cnames"
	annotationCertIssuers  = "router.tsuru.io/cert-issuers"
)

var (
	_ router.Router       = &GatewayAPIService{}
	_ router.RouterStatus = &GatewayAPIService{}

	defaultGatewayOptsAsAnnotations     = map[string]string{}
	defaultGatewayOptsAsAnnotationsDocs = map[string]string{}
)

// GatewayAPIService manages HTTPRoute resources using the Kubernetes Gateway API.
type GatewayAPIService struct {
	*BaseService
	GatewayName           string
	GatewayNamespace      string
	DomainSuffix          string
	AcmeIssuer            string
	GatewayClient         gatewayclient.Interface
	OptsAsAnnotations     map[string]string
	OptsAsAnnotationsDocs map[string]string
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
// One HTTPRoute is created per prefix. When ExposeAllServices is false, only the "default" prefix is used.
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

	if g.GatewayName == "" {
		err := fmt.Errorf("gateway name must be specified via startup flags or X-Gateway-Name header")
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
		ns:              ns,
		backendTargets:  backendTargets,
		backendServices: backendServices,
		isHTTPOnly:      o.Opts.HTTPOnly,
	}

	// Build prefix list from resolved backends (already filtered by getBackendTargets).
	prefixes := make([]string, 0, len(rc.backendServices))
	for prefixString := range rc.backendServices {
		prefixes = append(prefixes, prefixString)
	}
	sort.Strings(prefixes)

	// Ensure HTTPRoutes for all prefixes
	desiredRouteNames, err := g.ensureHTTPRoutes(ctx, span, client, id, o, rc, prefixes)
	if err != nil {
		return err
	}

	// Clean up obsolete routes
	err = g.cleanupHTTPRoutes(ctx, span, client, rc.ns, id, desiredRouteNames)
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
	isHTTPOnly      bool
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

func (g *GatewayAPIService) buildHTTPRouteRule(path string, svc *corev1.Service) gatewayv1.HTTPRouteRule {
	port := gatewayv1.PortNumber(defaultServicePort)
	if len(svc.Spec.Ports) > 0 {
		port = gatewayv1.PortNumber(svc.Spec.Ports[0].Port)
	}

	pathType := gatewayv1.PathMatchPathPrefix

	return gatewayv1.HTTPRouteRule{

		Matches: []gatewayv1.HTTPRouteMatch{
			{
				Path: &gatewayv1.HTTPPathMatch{
					Type:  &pathType,
					Value: &path,
				},
			},
		},
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

// mergeAnnotations preserves existing annotations not set by the new object.
func mergeAnnotations(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]string)
	}
	for k, v := range src {
		if _, exists := dst[k]; !exists {
			dst[k] = v
		}
	}
	return dst
}

func isFrozenHTTPRoute(httpRoute *gatewayv1.HTTPRoute) bool {
	if httpRoute == nil {
		return false
	}
	return httpRoute.Annotations[AnnotationFreeze] == "true"
}

func (g *GatewayAPIService) getExistingHTTPRoute(ctx context.Context, span opentracing.Span, client gatewayclient.Interface, ns, routeName string) (*gatewayv1.HTTPRoute, error) {
	existingHTTPRoute, err := client.GatewayV1().HTTPRoutes(ns).Get(ctx, routeName, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil, nil
		}
		setSpanError(span, err)
		return nil, err
	}
	return existingHTTPRoute, nil
}

func (g *GatewayAPIService) upsertHTTPRoute(ctx context.Context, span opentracing.Span, client gatewayclient.Interface, ns string, httpRoute, existingHTTPRoute *gatewayv1.HTTPRoute) error {
	var err error
	if existingHTTPRoute == nil {
		_, err = client.GatewayV1().HTTPRoutes(ns).Create(ctx, httpRoute, metav1.CreateOptions{})
	} else {
		httpRoute.ResourceVersion = existingHTTPRoute.ResourceVersion
		httpRoute.Annotations = mergeAnnotations(httpRoute.Annotations, existingHTTPRoute.Annotations)
		_, err = client.GatewayV1().HTTPRoutes(ns).Update(ctx, httpRoute, metav1.UpdateOptions{})
	}
	if err != nil {
		setSpanError(span, err)
		return err
	}
	return nil
}

func (g *GatewayAPIService) cleanupHTTPRoutes(ctx context.Context, span opentracing.Span, client gatewayclient.Interface, ns string, id router.InstanceID, desiredRouteNames map[string]bool) error {
	existingRoutes, err := g.listHTTPRoutesForApp(ctx, client, ns, id)
	if err != nil {
		setSpanError(span, err)
		return err
	}

	for _, route := range existingRoutes {
		if !desiredRouteNames[route.Name] && !isFrozenHTTPRoute(&route) && route.Labels[labelCNameHTTPRoute] != "true" {
			err = client.GatewayV1().HTTPRoutes(ns).Delete(ctx, route.Name, metav1.DeleteOptions{})
			if err != nil && !k8sErrors.IsNotFound(err) {
				setSpanError(span, err)
				return err
			}
		}
	}

	return nil
}

// ensureHTTPRoutes creates or updates HTTPRoutes.
// Returns the map of desired route names for cleanup.
func (g *GatewayAPIService) ensureHTTPRoutes(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	rc httpRouteContext,
	prefixes []string,
) (map[string]bool, error) {
	desiredRouteNames := map[string]bool{}

	path := o.Opts.Route
	if path == "" {
		path = "/"
	}

	for _, prefixString := range prefixes {
		svc := rc.backendServices[prefixString]
		target := rc.backendTargets[prefixString]

		routeName := g.httpRouteNameForPrefix(id, prefixString)
		desiredRouteNames[routeName] = true

		existingHTTPRoute, err := g.getExistingHTTPRoute(ctx, span, client, rc.ns, routeName)
		if err != nil {
			return nil, err
		}

		if isFrozenHTTPRoute(existingHTTPRoute) {
			log.Printf("HTTPRoute is frozen, skipping: %s/%s", existingHTTPRoute.Namespace, existingHTTPRoute.Name)
			continue
		}

		host := g.buildHTTPRouteHostname(prefixString, id, o, g.DomainSuffix)

		gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)
		labels, annotations := g.buildHTTPRouteLabelsAndAnnotations(
			map[string]string{
				routerInstanceLabel:          id.InstanceName,
				appBaseServiceNamespaceLabel: target.Namespace,
				appBaseServiceNameLabel:      target.Service,
				labelHTTPRouteHTTPOnly:       fmt.Sprintf("%t", rc.isHTTPOnly),
			},
			o.Opts,
			id,
			o.Team,
			o.Tags,
		)
		httpRoute := &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:        routeName,
				Namespace:   rc.ns,
				Labels:      labels,
				Annotations: annotations,
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
				Rules:     []gatewayv1.HTTPRouteRule{g.buildHTTPRouteRule(path, svc)},
			},
		}

		err = g.upsertHTTPRoute(ctx, span, client, rc.ns, httpRoute, existingHTTPRoute)
		if err != nil {
			return nil, err
		}
	}

	return desiredRouteNames, nil
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

	schema := "http"
	if len(routes) > 0 && routes[0].Labels[labelHTTPRouteHTTPOnly] != "true" {
		schema = "https"
	}

	var addresses []string
	for _, route := range routes {
		for _, hostname := range route.Spec.Hostnames {
			addresses = append(addresses, fmt.Sprintf("%s://%s", schema, hostname))
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
	opts := map[string]string{
		router.Domain:      "Domain used on router.",
		router.Route:       "Path used on router rule.",
		router.AllPrefixes: "",
	}
	docs := mergeMaps(defaultGatewayOptsAsAnnotationsDocs, g.OptsAsAnnotationsDocs)
	for _, k := range []string{router.Domain, router.Route, router.AllPrefixes} {
		if docs[k] != "" {
			opts[k] = docs[k]
		}
	}
	for k, v := range mergeMaps(defaultGatewayOptsAsAnnotations, g.OptsAsAnnotations) {
		opts[k] = v
		if docs[k] != "" {
			opts[k] = docs[k]
		}
	}
	return opts
}

func (g *GatewayAPIService) buildHTTPRouteLabelsAndAnnotations(baseLabels map[string]string, routerOpts router.Opts, id router.InstanceID, team string, tags []string) (map[string]string, map[string]string) {
	labels := g.buildLabels(baseLabels, routerOpts, id, team, tags)
	annotations := g.buildAnnotations(routerOpts, id, team, tags)

	return labels, annotations
}

// buildLabelsAndAnnotations merges base labels/annotations with router-level Labels/Annotations,
func (g *GatewayAPIService) buildAnnotations(routerOpts router.Opts, id router.InstanceID, team string, tags []string) map[string]string {
	annotations := map[string]string{}

	for k, v := range g.Annotations {
		annotations[k] = v
	}

	optsAsAnnotations := mergeMaps(defaultGatewayOptsAsAnnotations, g.OptsAsAnnotations)
	for optName, optValue := range routerOpts.AdditionalOpts {
		annotationName, ok := optsAsAnnotations[optName]
		if !ok {
			if !strings.Contains(optName, "/") {
				continue
			}
			annotationName = optName
		}
		if strings.HasSuffix(annotationName, "-") {
			delete(annotations, strings.TrimSuffix(annotationName, "-"))
		} else {
			annotations[annotationName] = optValue
		}
	}

	return annotations
}

func (g *GatewayAPIService) buildLabels(baseLabels map[string]string, routerOpts router.Opts, id router.InstanceID, team string, tags []string) map[string]string {
	labels := map[string]string{}

	for k, v := range baseLabels {
		labels[k] = v
	}
	for k, v := range g.Labels {
		labels[k] = v
	}

	labels[appLabel] = id.AppName
	labels[teamLabel] = team

	for _, tag := range tags {
		parts := strings.SplitN(tag, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		if key == "" {
			continue
		}
		labelName := customTagPrefixLabel + key
		if len(validation.IsQualifiedName(labelName)) > 0 {
			continue
		}
		labels[labelName] = value
	}
	return labels
}

// One ListenerSet is created per CName so each can carry its own cert-manager.io/common-name.
func (g *GatewayAPIService) listenerSetName(id router.InstanceID, cname string) string {
	sanitized := strings.ReplaceAll(cname, ".", "-")
	sanitized = strings.ReplaceAll(sanitized, "*", "wildcard")
	base := fmt.Sprintf("kubernetes-router-%s-%s-ls", id.AppName, sanitized)
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
// It creates one ListenerSet per CName plus a CName HTTPRoute for each.
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
				routerOpts:    o.Opts,
				tags:          o.Tags,
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
		return g.cleanupOrphanedListenerSets(ctx, client, id, ns, nil)
	}

	// One ListenerSet per CName so cert-manager can set a per-certificate common-name.
	for _, cname := range o.CNames {
		issuer := o.CertIssuers[cname]
		if issuer == "" {
			issuer = g.AcmeIssuer
		}
		if issuer == "" {
			// Without an issuer there is no TLS certificate to provision, so skip.
			continue
		}

		err := g.ensureListenerSet(ctx, span, client, id, o, ns, issuer, cname)
		if err != nil {
			return err
		}

		lsNamespace := gatewayv1.Namespace(ns)
		lsGroup := gatewayv1.Group("gateway.networking.k8s.io")
		lsKind := gatewayv1.Kind("ListenerSet")
		sectionName := listenerEntryName(cname)
		parentRefs := []gatewayv1.ParentReference{
			{
				Group:       &lsGroup,
				Kind:        &lsKind,
				Name:        gatewayv1.ObjectName(g.listenerSetName(id, cname)),
				Namespace:   &lsNamespace,
				SectionName: &sectionName,
			},
		}

		err = g.ensureCNameHTTPRoute(ctx, span, client, ensureCNameHTTPRouteOpts{
			id:            id,
			ns:            ns,
			cname:         cname,
			team:          o.Team,
			defaultTarget: defaultTarget,
			parentRefs:    parentRefs,
			routerOpts:    o.Opts,
			tags:          o.Tags,
		})
		if err != nil {
			return err
		}
	}

	// Remove CNames that are no longer desired
	for _, cname := range cnamesToRemove {
		err := g.removeCNameHTTPRoute(ctx, client, id, ns, cname)
		if err != nil {
			return err
		}
	}

	// Clean up ListenerSets whose CName is no longer desired
	err := g.cleanupOrphanedListenerSets(ctx, client, id, ns, o.CNames)
	if err != nil {
		return err
	}

	return nil
}

// ensureListenerSet creates or updates the ListenerSet that holds the single TLS
// listener for a given CName. Keeping one CName per ListenerSet lets each one be
// annotated with its own cert-manager.io/common-name.
func (g *GatewayAPIService) ensureListenerSet(
	ctx context.Context,
	span opentracing.Span,
	client gatewayclient.Interface,
	id router.InstanceID,
	o router.EnsureBackendOpts,
	ns, issuer, cname string,
) error {
	lsName := g.listenerSetName(id, cname)
	gwNamespace := gatewayv1.Namespace(g.GatewayNamespace)

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

	baseLabels := map[string]string{
		routerInstanceLabel: id.InstanceName,
		labelCertIssuer:     issuer,
	}
	labels := g.buildLabels(baseLabels, o.Opts, id, o.Team, o.Tags)

	listenerSet := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        lsName,
			Namespace:   ns,
			Labels:      labels,
			Annotations: g.listenerSetCertManagerAnnotations(issuer, cname),
		},
		Spec: gatewayv1.ListenerSetSpec{
			ParentRef: gatewayv1.ParentGatewayReference{
				Name:      gatewayv1.ObjectName(g.GatewayName),
				Namespace: &gwNamespace,
			},
			Listeners: []gatewayv1.ListenerEntry{listener},
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
	routerOpts    router.Opts
	tags          []string
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

	labels, annotations := g.buildHTTPRouteLabelsAndAnnotations(
		map[string]string{
			routerInstanceLabel: opts.id.InstanceName,
			labelCNameHTTPRoute: "true",
		},
		opts.routerOpts,
		opts.id,
		opts.team,
		opts.tags,
	)
	httpRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        routeName,
			Namespace:   opts.ns,
			Labels:      labels,
			Annotations: annotations,
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
	httpRoute.Annotations = mergeAnnotations(httpRoute.Annotations, existing.Annotations)
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

// cleanupOrphanedListenerSets removes ListenerSets whose CName is no longer desired.
func (g *GatewayAPIService) cleanupOrphanedListenerSets(
	ctx context.Context,
	client gatewayclient.Interface,
	id router.InstanceID,
	ns string,
	requestedCNames []string,
) error {
	selector := labels.Set{
		appLabel:            id.AppName,
		routerInstanceLabel: id.InstanceName,
	}.AsSelector()

	existingListenerSets, err := client.GatewayV1().ListenerSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return err
	}

	// Build the set of ListenerSet names that should still exist.
	desired := map[string]bool{}
	for _, cname := range requestedCNames {
		desired[g.listenerSetName(id, cname)] = true
	}

	for _, ls := range existingListenerSets.Items {
		if !desired[ls.Name] {
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
// The caller only invokes this with a non-empty issuer and CName. Because each ListenerSet
// holds a single CName, cert-manager.io/common-name is set to that CName so the issued
// Certificate carries the correct CN.
func (g *GatewayAPIService) listenerSetCertManagerAnnotations(issuer, cname string) map[string]string {
	var annotations map[string]string

	if parts := strings.SplitN(issuer, ".", 3); len(parts) == 3 {
		annotations = map[string]string{
			certManagerIssuerKey:      parts[0],
			certManagerIssuerKindKey:  parts[1],
			certManagerIssuerGroupKey: parts[2],
		}
	} else {
		annotations = map[string]string{
			certManagerClusterIssuerKey: issuer,
		}
	}

	annotations[certManagerCommonName] = cname

	return annotations
}
