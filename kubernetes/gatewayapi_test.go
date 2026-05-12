package kubernetes

import (
	"testing"

	"github.com/opentracing/opentracing-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsuru/kubernetes-router/router"
	faketsuru "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	fakeapiextensions "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func newFakeGatewayAPIService() (*GatewayAPIService, *gatewayfake.Clientset) {
	// Creates a GatewayAPIService wired with fake Kubernetes and Gateway API clients.
	gwClient := gatewayfake.NewSimpleClientset()
	return &GatewayAPIService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           k8sfake.NewSimpleClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewSimpleClientset(),
		},
		GatewayName:      "main-gw",
		GatewayNamespace: "default",
		DomainSuffix:     "local",
		GatewayClient:    gwClient,
	}, gwClient
}

func TestGatewayAPIServiceBuildHTTPRouteHostname(t *testing.T) {
	// Validates hostname precedence and formatting for default and prefixed backends.
	svc, _ := newFakeGatewayAPIService()
	id := idForApp("myapp")

	tests := []struct {
		name         string
		prefix       string
		opts         router.Opts
		domainSuffix string
		expected     string
	}{
		{
			name:         "uses explicit domain",
			prefix:       "default",
			opts:         router.Opts{Domain: "apps.example.com"},
			domainSuffix: "local",
			expected:     "apps.example.com",
		},
		{
			name:         "uses app and suffix for default prefix",
			prefix:       "default",
			opts:         router.Opts{},
			domainSuffix: "cluster.local",
			expected:     "myapp.cluster.local",
		},
		{
			name:         "uses domain prefix when provided",
			prefix:       "default",
			opts:         router.Opts{DomainPrefix: "staging"},
			domainSuffix: "cluster.local",
			expected:     "staging.myapp.cluster.local",
		},
		{
			name:         "adds backend prefix for non default service",
			prefix:       "v1",
			opts:         router.Opts{},
			domainSuffix: "cluster.local",
			expected:     "v1.myapp.cluster.local",
		},
		{
			name:         "non-default prefix prepends to explicit domain",
			prefix:       "v2",
			opts:         router.Opts{Domain: "fixed.example.com"},
			domainSuffix: "cluster.local",
			expected:     "v2.fixed.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := svc.buildHTTPRouteHostname(tt.prefix, id, router.EnsureBackendOpts{Opts: tt.opts}, tt.domainSuffix)
			assert.Equal(t, tt.expected, host)
		})
	}
}

func TestListenerEntryName(t *testing.T) {
	// Ensures listener section names are sanitized and trimmed to Kubernetes limits.
	// Act + Assert: replacement rules for dots and wildcards.
	assert.Equal(t, gatewayv1.SectionName("a-b-com"), listenerEntryName("a.b.com"))
	assert.Equal(t, gatewayv1.SectionName("wildcard-example-com"), listenerEntryName("*.example.com"))

	// Ensure a short, already-valid name passes through unchanged.
	assert.Equal(t, gatewayv1.SectionName("valid-name"), listenerEntryName("valid-name"))

	// Act: generate a long section name.
	long := "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz.example.com"
	name := listenerEntryName(long)
	// Assert: listener names should obey the 63-char limit.
	assert.Len(t, string(name), 63)
}

func TestGatewayAPIServiceListenerSetCertManagerAnnotations(t *testing.T) {
	// Covers annotation generation for default, cluster, and external issuer formats.
	tests := []struct {
		name       string
		acmeIssuer string
		issuer     string
		expected   map[string]string
	}{
		{
			name:       "uses acme issuer for empty issuer",
			acmeIssuer: "letsencrypt",
			issuer:     "",
			expected: map[string]string{
				certManagerClusterIssuerKey: "letsencrypt",
			},
		},
		{
			name:       "uses explicit cluster issuer",
			acmeIssuer: "",
			issuer:     "my-cluster-issuer",
			expected: map[string]string{
				certManagerClusterIssuerKey: "my-cluster-issuer",
			},
		},
		{
			name:       "uses external issuer format",
			acmeIssuer: "",
			issuer:     "foo.MyIssuer.my-group.io",
			expected: map[string]string{
				certManagerIssuerKey:      "foo",
				certManagerIssuerKindKey:  "MyIssuer",
				certManagerIssuerGroupKey: "my-group.io",
			},
		},
		{
			name:       "returns nil when both issuer and acmeIssuer are empty",
			acmeIssuer: "",
			issuer:     "",
			expected:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newFakeGatewayAPIService()
			svc.AcmeIssuer = tt.acmeIssuer
			assert.Equal(t, tt.expected, svc.listenerSetCertManagerAnnotations(tt.issuer))
		})
	}
}

func TestGatewayAPIServiceEnsureCreatesHTTPRoute(t *testing.T) {
	// Ensures Ensure creates a primary HTTPRoute with expected hostname, labels, and parent ref.
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	// Act: reconcile desired state for the app.
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
		Team: "my-team",
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Assert: verify route shape and linkage to the configured Gateway.
	routeName := svc.httpRouteName(idForApp("myapp"))
	route, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, route.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("myapp.local"), route.Spec.Hostnames[0])
	assert.Equal(t, "false", route.Labels[labelHTTPRouteHTTPOnly])
	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("main-gw"), route.Spec.ParentRefs[0].Name)
	require.NotNil(t, route.Spec.ParentRefs[0].Namespace)
	assert.Equal(t, gatewayv1.Namespace("default"), *route.Spec.ParentRefs[0].Namespace)

	// Assert: backend ref targets the app service on the expected port.
	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("myapp-web"), route.Spec.Rules[0].BackendRefs[0].Name)
	require.NotNil(t, route.Spec.Rules[0].BackendRefs[0].Port)
	assert.Equal(t, gatewayv1.PortNumber(defaultServicePort), *route.Spec.Rules[0].BackendRefs[0].Port)
}

func TestGatewayAPIServiceEnsureSkipsFrozenHTTPRoute(t *testing.T) {
	// Verifies frozen routes are not modified by Ensure.
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	// Arrange: pre-create a frozen route with an old hostname.
	routeName := svc.httpRouteName(idForApp("myapp"))
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: "default",
			Labels: map[string]string{
				appLabel:            "myapp",
				routerInstanceLabel: "",
			},
			Annotations: map[string]string{AnnotationFreeze: "true"},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"old.myapp.local"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Act: run Ensure with different desired state.
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Assert: frozen route remains unchanged.
	route, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, route.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("old.myapp.local"), route.Spec.Hostnames[0])
}

func TestGatewayAPIServiceUpsertHTTPRoutePreservesAnnotations(t *testing.T) {
	// Confirms update path keeps existing annotations when upserting a route.
	svc, gwClient := newFakeGatewayAPIService()
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	// Arrange: create an existing route with metadata that must be preserved.
	existing, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "route1",
			Namespace:   "default",
			Annotations: map[string]string{"x": "1"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Act: upsert with a route object that does not contain annotations.
	err = svc.upsertHTTPRoute(ctx, span, gwClient, "default", &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "route1",
			Namespace: "default",
		},
	}, existing)
	require.NoError(t, err)

	// Assert: previous annotations are still present.
	updated, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, "route1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "1", updated.Annotations["x"])
}

func TestGatewayAPIServiceEnsureCNamesHTTPOnly(t *testing.T) {
	// In HTTP-only mode, CName routes should point directly to the Gateway and stale CNames should be removed
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	// Arrange: pre-seed the main route annotation and one stale CName route.
	id := idForApp("myapp")
	mainRouteName := svc.httpRouteName(id)
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        mainRouteName,
			Namespace:   "default",
			Annotations: map[string]string{annotationCNames: "old.example.com"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.httpRouteCNameName(id, "old.example.com"),
			Namespace: "default",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Act: run CName reconciliation with a new desired set.
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()
	err = svc.ensureCNames(ctx, span, gwClient, id, router.EnsureBackendOpts{
		Opts:   router.Opts{HTTPOnly: true},
		CNames: []string{"new.example.com"},
		Team:   "my-team",
	}, "default", router.BackendTarget{Service: "myapp-web", Namespace: "default"})
	require.NoError(t, err)

	// Assert: new route exists and points to the Gateway.
	newRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteCNameName(id, "new.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, newRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("main-gw"), newRoute.Spec.ParentRefs[0].Name)

	// Assert: stale route was removed.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteCNameName(id, "old.example.com"), metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err), "stale CName route should have been removed")
}

func TestGatewayAPIServiceEnsureCNamesWithIssuerGroups(t *testing.T) {
	// Non HTTP-only mode groups CNames by issuer and creates one ListenerSet per issuer group.
	svc, gwClient := newFakeGatewayAPIService()
	svc.AcmeIssuer = "letsencrypt"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	// Act: run CName reconciliation with a mix of default issuer and explicit cluster and external issuers.
	err = svc.ensureCNames(ctx, span, gwClient, id, router.EnsureBackendOpts{
		CNames: []string{"a.example.com", "b.example.com"},
		CertIssuers: map[string]string{
			"a.example.com": "custom-issuer",
		},
		Team: "my-team",
	}, "default", router.BackendTarget{Service: "myapp-web", Namespace: "default"})
	require.NoError(t, err)

	// Assert: one ListenerSet for explicit issuer and another for default Acme issuer.
	_, err = gwClient.GatewayV1().ListenerSets("default").Get(ctx, svc.listenerSetName(id, "custom-issuer"), metav1.GetOptions{})
	require.NoError(t, err)

	_, err = gwClient.GatewayV1().ListenerSets("default").Get(ctx, svc.listenerSetName(id, "letsencrypt"), metav1.GetOptions{})
	require.NoError(t, err)

	// Assert: CName route references the correct ListenerSet parent.
	aRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteCNameName(id, "a.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, aRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName(svc.listenerSetName(id, "custom-issuer")), aRoute.Spec.ParentRefs[0].Name)
}

func TestGatewayAPIServiceGetAddresses(t *testing.T) {
	// GetAddresses should map the route label to protocol and return hostnames as URLs.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")
	routeName := svc.httpRouteName(id)

	// Arrange: create a managed HTTPRoute with protocol label and hostname.
	_, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: "default",
			Labels: map[string]string{
				appLabel:               "myapp",
				routerInstanceLabel:    "",
				labelHTTPRouteHTTPOnly: "false",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"myapp.local"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Act
	addresses, err := svc.GetAddresses(ctx, id)
	// Assert
	require.NoError(t, err)
	assert.Equal(t, []string{"https://myapp.local"}, addresses)
}

func TestGatewayAPIServiceGetStatusReady(t *testing.T) {
	// A route with Accepted=True on parents should be reported as ready.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")
	routeName := svc.httpRouteName(id)

	// Arrange: create a route with an Accepted condition in status.
	_, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: "default",
			Labels: map[string]string{
				appLabel:            "myapp",
				routerInstanceLabel: "",
			},
		},
		Status: gatewayv1.HTTPRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{
				Parents: []gatewayv1.RouteParentStatus{
					{
						Conditions: []metav1.Condition{
							{Type: "Accepted", Status: metav1.ConditionTrue},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	status, detail, err := svc.GetStatus(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, router.BackendStatusReady, status)
	assert.Equal(t, "", detail)
}

func TestGatewayAPIServiceBuildHTTPRouteRuleDefaultPort(t *testing.T) {
	// Route backend ref should default to the service default port when no explicit port exists.
	svc, _ := newFakeGatewayAPIService()
	rule := svc.buildHTTPRouteRule(&corev1.Service{
		Spec: corev1.ServiceSpec{},
	})

	require.Len(t, rule.BackendRefs, 1)
	require.NotNil(t, rule.BackendRefs[0].BackendRef.Port)
	assert.Equal(t, gatewayv1.PortNumber(defaultServicePort), *rule.BackendRefs[0].BackendRef.Port)
}

func TestGatewayAPIServiceBuildHTTPRouteRuleWithPort(t *testing.T) {
	// When the service exposes an explicit port, that port should appear in the backend ref.
	svc, _ := newFakeGatewayAPIService()

	// Arrange: service with an explicit port declaration.
	rule := svc.buildHTTPRouteRule(&corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "mysvc"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 1234}},
		},
	})

	// Assert: backend ref uses the declared port, not the default.
	require.Len(t, rule.BackendRefs, 1)
	require.NotNil(t, rule.BackendRefs[0].BackendRef.Port)
	assert.Equal(t, gatewayv1.PortNumber(1234), *rule.BackendRefs[0].BackendRef.Port)
	assert.Equal(t, gatewayv1.ObjectName("mysvc"), rule.BackendRefs[0].BackendRef.Name)
}

func TestGatewayAPIServiceUpsertHTTPRouteWhenCreate(t *testing.T) {
	// When existingHTTPRoute is nil, upsertHTTPRoute should create a new route.
	svc, gwClient := newFakeGatewayAPIService()
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	// Act: upsert with no existing route (create path).
	err := svc.upsertHTTPRoute(ctx, span, gwClient, "default", &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "new-route",
			Namespace: "default",
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"app.local"},
		},
	}, nil)
	require.NoError(t, err)

	// Assert: the route was actually created in the fake client.
	created, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, "new-route", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, created.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("app.local"), created.Spec.Hostnames[0])
}

func TestGatewayAPIServiceEnsureFailsWithoutGatewayName(t *testing.T) {
	// Ensure should return an error when no GatewayName is configured.
	svc, _ := newFakeGatewayAPIService()
	svc.GatewayName = "" // clear startup default
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	// Act: call Ensure without providing a gateway name via opts.
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})

	// Assert: descriptive error is returned.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gateway name")
}

func TestGatewayAPIServiceEnsureOverridesDomainSuffixFromOpts(t *testing.T) {
	// When opts.DomainSuffix is set, the resulting hostname must use it instead of the service default.
	svc, gwClient := newFakeGatewayAPIService()
	svc.DomainSuffix = "default.local"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	// Act: Ensure with an override domain suffix.
	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{
			GatewayName:      "main-gw",
			GatewayNamespace: "default",
			DomainSuffix:     "override.io",
		},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Assert: hostname reflects the opts override, not the service-level default.
	routeName := svc.httpRouteName(idForApp("myapp"))
	route, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, route.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("myapp.override.io"), route.Spec.Hostnames[0])
}

func TestGatewayAPIServiceCleanupRemovesStaleRoutes(t *testing.T) {
	// A route created for a prefix that is absent on the second Ensure call must be deleted.
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")

	// Arrange: first Ensure creates routes for both default and "v1" prefixes.
	err = svc.Ensure(ctx, id, router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default", ExposeAllServices: true},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
			{Prefix: "v1", Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	v1RouteName := svc.httpRouteNameForPrefix(id, "v1")
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, v1RouteName, metav1.GetOptions{})
	require.NoError(t, err, "v1 route should exist after first Ensure")

	// Act: second Ensure without the "v1" prefix — stale route must be cleaned up.
	err = svc.Ensure(ctx, id, router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Assert: the v1 route was deleted.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, v1RouteName, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err), "stale v1 route should have been removed")
}

func TestGatewayAPIServiceRemove(t *testing.T) {
	// Remove should delete main HTTPRoutes, CName HTTPRoutes, and ListenerSets for the app.
	svc, gwClient := newFakeGatewayAPIService()
	svc.AcmeIssuer = "letsencrypt"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")

	// Arrange: Ensure creates main HTTPRoute, CName HTTPRoute and ListenerSet.
	err = svc.Ensure(ctx, id, router.EnsureBackendOpts{
		Opts:   router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
		Team:   "my-team",
		CNames: []string{"alias.example.com"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	mainRoute := svc.httpRouteName(id)
	cnameRoute := svc.httpRouteCNameName(id, "alias.example.com")
	listenerSetName := svc.listenerSetName(id, "letsencrypt")

	// Act: remove all app resources.
	err = svc.Remove(ctx, id)
	require.NoError(t, err)

	// Assert: main route is gone.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, mainRoute, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err), "main HTTPRoute should be deleted")

	// Assert: CName route is gone.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, cnameRoute, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err), "CName HTTPRoute should be deleted")

	// Assert: ListenerSet is gone.
	_, err = gwClient.GatewayV1().ListenerSets("default").Get(ctx, listenerSetName, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err), "ListenerSet should be deleted")
}

func TestGatewayAPIServiceGetAddressesHTTPOnly(t *testing.T) {
	// When labelHTTPRouteHTTPOnly is "true", addresses should use the http:// scheme.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")

	// Arrange: create a route labeled as HTTP-only.
	_, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.httpRouteName(id),
			Namespace: "default",
			Labels: map[string]string{
				appLabel:               "myapp",
				routerInstanceLabel:    "",
				labelHTTPRouteHTTPOnly: "true",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"myapp.local"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	addresses, err := svc.GetAddresses(ctx, id)

	// Assert: scheme is http, not https.
	require.NoError(t, err)
	assert.Equal(t, []string{"http://myapp.local"}, addresses)
}

func TestGatewayAPIServiceGetAddressesNoRoutes(t *testing.T) {
	// When no routes exist at all GetAddresses should return nil without error.
	svc, _ := newFakeGatewayAPIService()
	id := idForApp("myapp")
	_ = createAppWebService(svc.Client, svc.Namespace, "myapp")

	// Act: no routes have been created.
	addresses, err := svc.GetAddresses(ctx, id)

	// Assert: graceful nil return.
	require.NoError(t, err)
	assert.Nil(t, addresses)
}

func TestGatewayAPIServiceGetStatusNotReady(t *testing.T) {
	// A route with Accepted=False should be reported as not-ready with the condition message.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")

	// Arrange: create a route whose Accepted condition is False.
	_, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.httpRouteName(id),
			Namespace: "default",
			Labels: map[string]string{
				appLabel:            "myapp",
				routerInstanceLabel: "",
			},
		},
		Status: gatewayv1.HTTPRouteStatus{
			RouteStatus: gatewayv1.RouteStatus{
				Parents: []gatewayv1.RouteParentStatus{
					{
						Conditions: []metav1.Condition{
							{Type: "Accepted", Status: metav1.ConditionFalse, Message: "no matching listener"},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	status, detail, err := svc.GetStatus(ctx, id)

	// Assert: not-ready with condition message propagated.
	require.NoError(t, err)
	assert.Equal(t, router.BackendStatusNotReady, status)
	assert.Equal(t, "no matching listener", detail)
}

func TestGatewayAPIServiceGetStatusWaitingForDeploy(t *testing.T) {
	// When no routes exist at all GetStatus should report not-ready "waiting for deploy".
	svc, _ := newFakeGatewayAPIService()
	id := idForApp("myapp")
	_ = createAppWebService(svc.Client, svc.Namespace, "myapp")

	// Act: no routes created yet.
	status, detail, err := svc.GetStatus(ctx, id)

	// Assert: not-ready with "waiting for deploy" detail.
	require.NoError(t, err)
	assert.Equal(t, router.BackendStatusNotReady, status)
	assert.Equal(t, "waiting for deploy", detail)
}

func TestGatewayAPIServiceEnsureCNameFrozenSkipped(t *testing.T) {
	// A CName HTTPRoute annotated with AnnotationFreeze must not be modified by ensureCNameHTTPRoute.
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")
	cname := "alias.example.com"
	routeName := svc.httpRouteCNameName(id, cname)

	// Arrange: pre-create a frozen CName route with a sentinel hostname.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:        routeName,
			Namespace:   "default",
			Annotations: map[string]string{AnnotationFreeze: "true"},
			Labels: map[string]string{
				appLabel:            "myapp",
				routerInstanceLabel: "",
				labelCNameHTTPRoute: "true",
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"frozen.example.com"},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Act: run CName reconciliation — frozen route must be ignored.
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()
	err = svc.ensureCNames(ctx, span, gwClient, id, router.EnsureBackendOpts{
		Opts:   router.Opts{HTTPOnly: true},
		CNames: []string{cname},
		Team:   "my-team",
	}, "default", router.BackendTarget{Service: "myapp-web", Namespace: "default"})
	require.NoError(t, err)

	// Assert: hostname was not overwritten.
	route, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, route.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("frozen.example.com"), route.Spec.Hostnames[0])
}
