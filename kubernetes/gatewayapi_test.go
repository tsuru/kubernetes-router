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
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayfake "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/fake"
)

func newFakeGatewayAPIService() (*GatewayAPIService, *gatewayfake.Clientset) {
	// Creates a GatewayAPIService wired with fake Kubernetes and Gateway API clients.
	gwClient := gatewayfake.NewClientset()
	return &GatewayAPIService{
		BaseService: &BaseService{
			Namespace:        "default",
			Client:           k8sfake.NewClientset(),
			TsuruClient:      faketsuru.NewSimpleClientset(),
			ExtensionsClient: fakeapiextensions.NewClientset(),
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

func TestGatewayAPIServiceListenerSetName(t *testing.T) {
	// The CName drives the name, with dots and wildcards sanitized for k8s.
	svc, _ := newFakeGatewayAPIService()
	id := idForApp("myapp")

	tests := []struct {
		name  string
		id    router.InstanceID
		cname string
		want  string
	}{
		{"simple cname", id, "app.example.com", "kube-router-myapp-app-example-com"},
		{"wildcard cname", id, "*.example.com", "kube-router-myapp-wildcard-example-com"},
		{
			"with router instance name appended",
			router.InstanceID{AppName: "jojo-app", InstanceName: "lab-https-gateway"},
			"app.example.com",
			"kube-router-jojo-app-app-example-com-lab-https-gateway",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, svc.listenerSetName(tt.id, tt.cname))
		})
	}
}

func TestGatewayAPIServiceListenerSetCertManagerAnnotations(t *testing.T) {
	// Covers annotation generation for cluster and external issuer formats. The caller
	// always resolves a non-empty issuer before invoking this, and every ListenerSet
	// gets a per-CName common-name annotation.
	tests := []struct {
		name     string
		issuer   string
		cname    string
		expected map[string]string
	}{
		{
			name:   "uses explicit cluster issuer",
			issuer: "my-cluster-issuer",
			cname:  "app.example.com",
			expected: map[string]string{
				certManagerClusterIssuerKey: "my-cluster-issuer",
				certManagerCommonName:       "app.example.com",
			},
		},
		{
			name:   "uses external issuer format",
			issuer: "foo.MyIssuer.my-group.io",
			cname:  "app.example.com",
			expected: map[string]string{
				certManagerIssuerKey:      "foo",
				certManagerIssuerKindKey:  "MyIssuer",
				certManagerIssuerGroupKey: "my-group.io",
				certManagerCommonName:     "app.example.com",
			},
		},
		{
			name:   "falls back to cluster issuer for partial external format",
			issuer: "foo.MyIssuer",
			cname:  "app.example.com",
			expected: map[string]string{
				certManagerClusterIssuerKey: "foo.MyIssuer",
				certManagerCommonName:       "app.example.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newFakeGatewayAPIService()
			assert.Equal(t, tt.expected, svc.listenerSetCertManagerAnnotations(tt.issuer, tt.cname))
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

func TestGatewayAPIServiceEnsureCNamesPerCNameListenerSets(t *testing.T) {
	// Non HTTP-only mode creates one ListenerSet per CName, each carrying its own
	// cert-manager.io/common-name and resolved issuer.
	svc, gwClient := newFakeGatewayAPIService()
	svc.AcmeIssuer = "letsencrypt"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	// Act: reconcile two CNames, one with an explicit issuer and one falling back to Acme.
	err = svc.ensureCNames(ctx, span, gwClient, id, router.EnsureBackendOpts{
		CNames: []string{"a.example.com", "b.example.com"},
		CertIssuers: map[string]string{
			"a.example.com": "custom-issuer",
		},
		Team: "my-team",
	}, "default", router.BackendTarget{Service: "myapp-web", Namespace: "default"})
	require.NoError(t, err)

	// Assert: a dedicated ListenerSet exists per CName, each with a single listener and
	// its own common-name annotation.
	lsA, err := gwClient.GatewayV1().ListenerSets("default").Get(ctx, svc.listenerSetName(id, "a.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, lsA.Spec.Listeners, 1)
	assert.Equal(t, "a.example.com", lsA.Annotations[certManagerCommonName])
	assert.Equal(t, "custom-issuer", lsA.Annotations[certManagerClusterIssuerKey])

	lsB, err := gwClient.GatewayV1().ListenerSets("default").Get(ctx, svc.listenerSetName(id, "b.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, lsB.Spec.Listeners, 1)
	assert.Equal(t, "b.example.com", lsB.Annotations[certManagerCommonName])
	assert.Equal(t, "letsencrypt", lsB.Annotations[certManagerClusterIssuerKey])

	// Assert: CName route references its own ListenerSet parent.
	aRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteCNameName(id, "a.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, aRoute.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName(svc.listenerSetName(id, "a.example.com")), aRoute.Spec.ParentRefs[0].Name)
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
	rule := svc.buildHTTPRouteRule("/", &corev1.Service{
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
	rule := svc.buildHTTPRouteRule("/", &corev1.Service{
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

func TestGatewayAPIServiceUpdateCNamesAnnotationRemovesWhenEmpty(t *testing.T) {
	// updateCNamesAnnotation should remove the annotation key when no CNames remain.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")
	routeName := svc.httpRouteName(id)

	_, err := gwClient.GatewayV1().HTTPRoutes("default").Create(ctx, &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: "default",
			Annotations: map[string]string{
				annotationCNames: "old.example.com",
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	svc.updateCNamesAnnotation(ctx, gwClient, id, "default", nil)

	route, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	_, found := route.Annotations[annotationCNames]
	assert.False(t, found)
}

func TestGatewayAPIServiceEnsureListenerSetUpdatesExisting(t *testing.T) {
	// ensureListenerSet should update an existing ListenerSet when called again for the same CName.
	svc, gwClient := newFakeGatewayAPIService()
	id := idForApp("myapp")
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	err := svc.ensureListenerSet(ctx, span, gwClient, id, router.EnsureBackendOpts{}, "ns-default", "custom-issuer", "a.example.com")
	require.NoError(t, err)

	err = svc.ensureListenerSet(ctx, span, gwClient, id, router.EnsureBackendOpts{}, "ns-default", "other-issuer", "a.example.com")
	require.NoError(t, err)

	ls, err := gwClient.GatewayV1().ListenerSets("ns-default").Get(ctx, svc.listenerSetName(id, "a.example.com"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, ls.Spec.Listeners, 1)
	require.NotNil(t, ls.Spec.Listeners[0].Hostname)
	assert.Equal(t, gatewayv1.Hostname("a.example.com"), *ls.Spec.Listeners[0].Hostname)
	// Update reflects the latest issuer and common-name.
	assert.Equal(t, "other-issuer", ls.Labels[labelCertIssuer])
	assert.Equal(t, "other-issuer", ls.Annotations[certManagerClusterIssuerKey])
	assert.Equal(t, "a.example.com", ls.Annotations[certManagerCommonName])
}

func TestGatewayAPIServiceEnsureListenerSetPropagatesLabels(t *testing.T) {
	svc, gwClient := newFakeGatewayAPIService()
	svc.Labels = map[string]string{"svc-label": "svc-value"}
	svc.Annotations = map[string]string{"svc-ann": "ann-value"}

	id := idForApp("jojo-app")
	span := opentracing.NoopTracer{}.StartSpan("test")
	defer span.Finish()

	o := router.EnsureBackendOpts{
		Team: "my-team",
		Tags: []string{"product=tsuru"},
		Opts: router.Opts{
			AdditionalOpts: map[string]string{
				"example.com/extra": "extra-value",
			},
		},
	}

	err := svc.ensureListenerSet(ctx, span, gwClient, id, o, "ns-default", "custom-issuer", "a.example.com")
	require.NoError(t, err)

	ls, err := gwClient.GatewayV1().ListenerSets("ns-default").Get(ctx, svc.listenerSetName(id, "a.example.com"), metav1.GetOptions{})
	require.NoError(t, err)

	// Labels: base (issuer + router-instance) + app + team + tag-derived + service-level.
	assert.Equal(t, "jojo-app", ls.Labels[appLabel])
	assert.Equal(t, "my-team", ls.Labels[teamLabel])
	assert.Equal(t, "custom-issuer", ls.Labels[labelCertIssuer])
	assert.Equal(t, id.InstanceName, ls.Labels[routerInstanceLabel])
	assert.Equal(t, "tsuru", ls.Labels[customTagPrefixLabel+"product"])
	assert.Equal(t, "svc-value", ls.Labels["svc-label"])

	// Annotations: only the cert-manager issuer annotation
	assert.Equal(t, "custom-issuer", ls.Annotations[certManagerClusterIssuerKey])
	_, hasSvcAnn := ls.Annotations["svc-ann"]
	assert.False(t, hasSvcAnn, "service-level annotations must not propagate to ListenerSet")
	_, hasExtra := ls.Annotations["example.com/extra"]
	assert.False(t, hasExtra, "AdditionalOpts annotations must not propagate to ListenerSet")
}

func TestGatewayAPIServiceEnsureFailsWithoutBackendTargets(t *testing.T) {
	// Ensure should fail with ErrNoBackendTarget when no prefixes are provided.
	svc, _ := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
	})

	assert.ErrorIs(t, err, ErrNoBackendTarget)
}

func TestGatewayAPIServiceEnsureFailsWhenBackendServiceMissing(t *testing.T) {
	// Ensure should return an error if the backend service referenced by target does not exist.
	svc, _ := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "service-that-does-not-exist", Namespace: "default"}},
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "service-that-does-not-exist")
}

func TestGatewayAPIServiceEnsureUsesAppNamespaceAsGatewayNamespace(t *testing.T) {
	// When opts.GatewayNamespace is empty, ParentRef namespace should default to app namespace.
	svc, gwClient := newFakeGatewayAPIService()
	svc.Namespace = "router-system"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	err = svc.Ensure(ctx, idForApp("myapp"), router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "router-system"}},
		},
	})
	require.NoError(t, err)

	routeName := svc.httpRouteName(idForApp("myapp"))
	route, err := gwClient.GatewayV1().HTTPRoutes("router-system").Get(ctx, routeName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, route.Spec.ParentRefs, 1)
	require.NotNil(t, route.Spec.ParentRefs[0].Namespace)
	assert.Equal(t, gatewayv1.Namespace("router-system"), *route.Spec.ParentRefs[0].Namespace)
}

func TestGatewayAPIServiceBuildHTTPRouteLabelsAndAnnotations(t *testing.T) {
	tests := []struct {
		name               string
		baseLabels         map[string]string
		routerOpts         router.Opts
		team               string
		tags               []string
		svcLabels          map[string]string
		svcAnnotations     map[string]string
		expectedLabels     map[string]string
		expectedAnnotation map[string]string
	}{
		{
			name:       "adds AdditionalOpts as annotations when key contains slash",
			baseLabels: map[string]string{"base": "label"},
			routerOpts: router.Opts{
				AdditionalOpts: map[string]string{
					"example.com/my-annotation": "value1",
					"another.io/config":         "value2",
				},
			},
			team: "my-team",
			expectedLabels: map[string]string{
				"base":    "label",
				appLabel:  "myapp",
				teamLabel: "my-team",
			},
			expectedAnnotation: map[string]string{
				"example.com/my-annotation": "value1",
				"another.io/config":         "value2",
			},
		},
		{
			name:       "ignores AdditionalOpts without slash",
			baseLabels: map[string]string{},
			routerOpts: router.Opts{
				AdditionalOpts: map[string]string{
					"no-slash-key": "ignored",
				},
			},
			team: "my-team",
			expectedLabels: map[string]string{
				appLabel:  "myapp",
				teamLabel: "my-team",
			},
			expectedAnnotation: map[string]string{},
		},
		{
			name:       "maps AdditionalOpts using OptsAsAnnotations",
			baseLabels: map[string]string{},
			routerOpts: router.Opts{
				AdditionalOpts: map[string]string{
					"custom-opt": "mapped-value",
				},
			},
			team:           "my-team",
			svcAnnotations: map[string]string{},
			expectedLabels: map[string]string{
				appLabel:  "myapp",
				teamLabel: "my-team",
			},
			expectedAnnotation: map[string]string{
				"example.com/my-annotation": "mapped-value",
			},
		},
		{
			name:       "removes annotation via suffix dash",
			baseLabels: map[string]string{},
			routerOpts: router.Opts{
				AdditionalOpts: map[string]string{
					"example.com/remove-me-": "",
				},
			},
			team:           "my-team",
			svcAnnotations: map[string]string{"example.com/remove-me": "old-value"},
			expectedLabels: map[string]string{
				appLabel:  "myapp",
				teamLabel: "my-team",
			},
			expectedAnnotation: map[string]string{},
		},
		{
			name:       "adds valid tags as custom labels",
			baseLabels: map[string]string{},
			routerOpts: router.Opts{},
			team:       "my-team",
			tags:       []string{"env=production", "tier=frontend"},
			expectedLabels: map[string]string{
				appLabel:                      "myapp",
				teamLabel:                     "my-team",
				customTagPrefixLabel + "env":  "production",
				customTagPrefixLabel + "tier": "frontend",
			},
			expectedAnnotation: map[string]string{},
		},
		{
			name:       "skips invalid tags",
			baseLabels: map[string]string{},
			routerOpts: router.Opts{},
			team:       "my-team",
			tags:       []string{"no-equals-sign", "=empty-key", "valid=ok"},
			expectedLabels: map[string]string{
				appLabel:                       "myapp",
				teamLabel:                      "my-team",
				customTagPrefixLabel + "valid": "ok",
			},
			expectedAnnotation: map[string]string{},
		},
		{
			name:           "merges service-level Labels and Annotations",
			baseLabels:     map[string]string{"base": "val"},
			routerOpts:     router.Opts{},
			team:           "my-team",
			svcLabels:      map[string]string{"svc-label": "svc-value"},
			svcAnnotations: map[string]string{"svc-ann": "ann-value"},
			expectedLabels: map[string]string{
				"base":      "val",
				"svc-label": "svc-value",
				appLabel:    "myapp",
				teamLabel:   "my-team",
			},
			expectedAnnotation: map[string]string{
				"svc-ann": "ann-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newFakeGatewayAPIService()
			svc.Labels = tt.svcLabels
			svc.Annotations = tt.svcAnnotations
			svc.OptsAsAnnotations = map[string]string{"custom-opt": "example.com/my-annotation"}

			id := idForApp("myapp")
			labels, annotations := svc.buildHTTPRouteLabelsAndAnnotations(tt.baseLabels, tt.routerOpts, id, tt.team, tt.tags)

			for k, v := range tt.expectedLabels {
				assert.Equal(t, v, labels[k], "label %s mismatch", k)
			}
			for k, v := range tt.expectedAnnotation {
				assert.Equal(t, v, annotations[k], "annotation %s mismatch", k)
			}
			// Verify removed annotations are not present
			if tt.routerOpts.AdditionalOpts != nil {
				for optName := range tt.routerOpts.AdditionalOpts {
					if len(optName) > 0 && optName[len(optName)-1] == '-' {
						stripped := optName[:len(optName)-1]
						_, found := annotations[stripped]
						assert.False(t, found, "annotation %s should have been removed", stripped)
					}
				}
			}
		})
	}
}

func TestGatewayAPIServiceSupportedOptions(t *testing.T) {
	svc, _ := newFakeGatewayAPIService()
	svc.OptsAsAnnotations = map[string]string{
		"my-opt":  "example.com/my-opt",
		"my-opt2": "example.com/my-opt2",
	}
	svc.OptsAsAnnotationsDocs = map[string]string{
		"my-opt2": "User friendly option description.",
	}

	options := svc.SupportedOptions(ctx)
	expectedOptions := map[string]string{
		router.Domain:      "Domain used on router.",
		router.Route:       "Path used on router rule.",
		router.AllPrefixes: "",
		"my-opt":           "example.com/my-opt",
		"my-opt2":          "User friendly option description.",
	}
	assert.Equal(t, expectedOptions, options)
}

func TestGatewayAPIServiceRemoveDeletesMultipleRoutes(t *testing.T) {
	// Remove should delete all labeled routes (multiple prefixes) and CName resources.
	svc, gwClient := newFakeGatewayAPIService()
	svc.AcmeIssuer = "letsencrypt"
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")

	// Arrange: Ensure with multiple prefixes and CNames.
	err = svc.Ensure(ctx, id, router.EnsureBackendOpts{
		Opts:   router.Opts{GatewayName: "main-gw", GatewayNamespace: "default", ExposeAllServices: true},
		Team:   "my-team",
		CNames: []string{"c1.example.com", "c2.example.com"},
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
			{Prefix: "api", Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Verify routes exist.
	defaultRoute := svc.httpRouteNameForPrefix(id, "default")
	apiRoute := svc.httpRouteNameForPrefix(id, "api")
	cnameRoute1 := svc.httpRouteCNameName(id, "c1.example.com")
	cnameRoute2 := svc.httpRouteCNameName(id, "c2.example.com")

	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, defaultRoute, metav1.GetOptions{})
	require.NoError(t, err)
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, apiRoute, metav1.GetOptions{})
	require.NoError(t, err)

	// Act
	err = svc.Remove(ctx, id)
	require.NoError(t, err)

	// Assert: all routes and resources are gone.
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, defaultRoute, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err))
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, apiRoute, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err))
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, cnameRoute1, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err))
	_, err = gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, cnameRoute2, metav1.GetOptions{})
	assert.True(t, k8sErrors.IsNotFound(err))
}

func TestGatewayAPIServiceEnsureWithExposeAllServices(t *testing.T) {
	// With ExposeAllServices=true, Ensure should create one HTTPRoute per prefix with distinct hostnames.
	svc, gwClient := newFakeGatewayAPIService()
	err := createAppWebService(svc.Client, svc.Namespace, "myapp")
	require.NoError(t, err)

	id := idForApp("myapp")

	// Act: Ensure with multiple prefixes and ExposeAllServices.
	err = svc.Ensure(ctx, id, router.EnsureBackendOpts{
		Opts: router.Opts{GatewayName: "main-gw", GatewayNamespace: "default", ExposeAllServices: true},
		Team: "my-team",
		Prefixes: []router.BackendPrefix{
			{Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
			{Prefix: "api", Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
			{Prefix: "worker", Target: router.BackendTarget{Service: "myapp-web", Namespace: "default"}},
		},
	})
	require.NoError(t, err)

	// Assert: default route uses app hostname.
	defaultRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteNameForPrefix(id, "default"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, defaultRoute.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("myapp.local"), defaultRoute.Spec.Hostnames[0])

	// Assert: api route uses prefix as subdomain.
	apiRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteNameForPrefix(id, "api"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, apiRoute.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("api.myapp.local"), apiRoute.Spec.Hostnames[0])

	// Assert: worker route uses prefix as subdomain.
	workerRoute, err := gwClient.GatewayV1().HTTPRoutes("default").Get(ctx, svc.httpRouteNameForPrefix(id, "worker"), metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, workerRoute.Spec.Hostnames, 1)
	assert.Equal(t, gatewayv1.Hostname("worker.myapp.local"), workerRoute.Spec.Hostnames[0])
}

func TestGatewayAPIServiceTlsSecretName(t *testing.T) {
	tests := []struct {
		name     string
		appName  string
		cname    string
		expected string
	}{
		{
			name:     "replaces dots with dashes",
			appName:  "myapp",
			cname:    "alias.example.com",
			expected: "myapp-alias-example-com-tls",
		},
		{
			name:     "replaces wildcard with literal",
			appName:  "myapp",
			cname:    "*.example.com",
			expected: "myapp-wildcard-example-com-tls",
		},
		{
			name:     "simple cname",
			appName:  "myapp",
			cname:    "simple",
			expected: "myapp-simple-tls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newFakeGatewayAPIService()
			id := idForApp(tt.appName)
			result := svc.tlsSecretName(id, tt.cname)
			assert.Equal(t, tt.expected, result)
		})
	}
}
