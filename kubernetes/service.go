// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	certmanagerv1clientset "github.com/cert-manager/cert-manager/pkg/client/clientset/versioned"
	"github.com/tsuru/kubernetes-router/observability"
	"github.com/tsuru/kubernetes-router/router"
	tsuruv1 "github.com/tsuru/tsuru/provision/kubernetes/pkg/apis/tsuru/v1"
	tsuruv1clientset "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

const (
	// managedServiceLabel is added to every service created by the router
	managedServiceLabel = "tsuru.io/router-lb"
	// externalServiceLabel should be added to every service with tsuru app
	// labels that are NOT created or managed by tsuru itself.
	externalServiceLabel = "tsuru.io/external-controller"

	appBaseServiceNamespaceLabel = "router.tsuru.io/base-service-namespace"
	appBaseServiceNameLabel      = "router.tsuru.io/base-service-name"
	routerFreezeLabel            = "router.tsuru.io/freeze"

	externalDNSHostnameLabel = "external-dns.alpha.kubernetes.io/hostname"

	defaultServicePort = 8888
	appLabel           = "tsuru.io/app-name"
	teamLabel          = "tsuru.io/team-name"
	domainLabel        = "tsuru.io/domain-name"
	processLabel       = "tsuru.io/app-process"
	appPoolLabel       = "tsuru.io/app-pool"

	appCRDName = "apps.tsuru.io"
)

var (
	ErrNoBackendTarget = errors.New("No default backend target found")
)

// ErrNoService indicates that the app has no service running
type ErrNoService struct{ App string }

func (e ErrNoService) Error() string {
	return fmt.Sprintf("no service found for app %q", e.App)
}

// BaseService has the base functionality needed by router.Service implementations
// targeting kubernetes
type BaseService struct {
	Namespace         string
	Timeout           time.Duration
	RestConfig        *rest.Config
	Client            kubernetes.Interface
	TsuruClient       tsuruv1clientset.Interface
	CertManagerClient certmanagerv1clientset.Interface
	ExtensionsClient  apiextensionsclientset.Interface
	Labels            map[string]string
	Annotations       map[string]string
}

// SupportedOptions returns the options supported by all services
func (k *BaseService) SupportedOptions(ctx context.Context) map[string]string {
	return nil
}

// Healthcheck uses the kubernetes client to check the connectivity
func (k *BaseService) Healthcheck(ctx context.Context) error {
	client, err := k.getClient()
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(k.Namespace).List(ctx, metav1.ListOptions{})
	return err
}

func (k *BaseService) getClient() (kubernetes.Interface, error) {
	if k.Client != nil {
		return k.Client, nil
	}
	config, err := k.getConfig()
	if err != nil {
		return nil, err
	}
	k.Client, err = kubernetes.NewForConfig(config)
	return k.Client, err
}

func (k *BaseService) getTsuruClient() (tsuruv1clientset.Interface, error) {
	if k.TsuruClient != nil {
		return k.TsuruClient, nil
	}
	config, err := k.getConfig()
	if err != nil {
		return nil, err
	}
	k.TsuruClient, err = tsuruv1clientset.NewForConfig(config)
	return k.TsuruClient, err
}

func (k BaseService) getCertManagerClient() (certmanagerv1clientset.Interface, error) {
	if k.CertManagerClient != nil {
		return k.CertManagerClient, nil
	}
	config, err := k.getConfig()
	if err != nil {
		return nil, err
	}
	return certmanagerv1clientset.NewForConfig(config)
}

func (k BaseService) getRestClient() {
}

func (k *BaseService) getExtensionsClient() (apiextensionsclientset.Interface, error) {
	if k.ExtensionsClient != nil {
		return k.ExtensionsClient, nil
	}
	config, err := k.getConfig()
	if err != nil {
		return nil, err
	}
	k.ExtensionsClient, err = apiextensionsclientset.NewForConfig(config)
	return k.ExtensionsClient, err
}

func (k *BaseService) getConfig() (*rest.Config, error) {
	if k.RestConfig != nil {
		return k.RestConfig, nil
	}
	var err error
	k.RestConfig, err = rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	k.RestConfig.Timeout = k.Timeout
	k.RestConfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return transport.DebugWrappers(observability.WrapTransport(rt))
	}
	return k.RestConfig, nil
}

func (k *BaseService) getWebService(ctx context.Context, appName string, target router.BackendTarget) (*apiv1.Service, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}

	svc, err := client.CoreV1().Services(target.Namespace).Get(ctx, target.Service, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil, ErrNoService{App: appName}
		}
		return nil, err
	}
	return svc, nil
}

func (k *BaseService) getApp(ctx context.Context, app string) (*tsuruv1.App, error) {
	hasCRD, err := k.hasCRD(ctx)
	if err != nil {
		return nil, err
	}
	if !hasCRD {
		return nil, nil
	}
	tclient, err := k.getTsuruClient()
	if err != nil {
		return nil, err
	}
	return tclient.TsuruV1().Apps(k.Namespace).Get(ctx, app, metav1.GetOptions{})
}

func (k *BaseService) getAppNamespace(ctx context.Context, appName string) (string, error) {
	app, err := k.getApp(ctx, appName)
	if err != nil {
		return "", err
	}
	if app == nil {
		return k.Namespace, nil
	}
	return app.Spec.NamespaceName, nil
}

func (k *BaseService) hasCRD(ctx context.Context) (bool, error) {
	eclient, err := k.getExtensionsClient()
	if err != nil {
		return false, err
	}
	_, err = eclient.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, appCRDName, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *BaseService) getDefaultBackendTarget(prefixes []router.BackendPrefix) (*router.BackendTarget, error) {
	for _, prefix := range prefixes {
		if prefix.Prefix == "" {
			return &prefix.Target, nil
		}
	}

	return nil, ErrNoBackendTarget
}

func addAllBackends(prefixes []router.BackendPrefix) map[string]router.BackendTarget {
	allTargets := map[string]router.BackendTarget{}
	for _, prefix := range prefixes {
		if prefix.Prefix == "" {
			allTargets["default"] = prefix.Target
			continue
		}
		prefixSanitized := strings.ReplaceAll(prefix.Prefix, "_", "-")
		allTargets[prefixSanitized] = prefix.Target
	}
	return allTargets
}

// getBackendTargets returns all targets pointed by the app services or only the base target according to the allBackends flag
func (s *BaseService) getBackendTargets(prefixes []router.BackendPrefix, allBackends bool) (map[string]router.BackendTarget, error) {
	allTargets := map[string]router.BackendTarget{}
	if allBackends {
		allTargets = addAllBackends(prefixes)
	} else {
		baseTarget, err := s.getDefaultBackendTarget(prefixes)
		if err != nil {
			return nil, err
		}
		if baseTarget != nil {
			allTargets["default"] = *baseTarget
		}
	}
	if len(allTargets) <= 0 {
		return nil, ErrNoBackendTarget
	}

	return allTargets, nil
}

func (s *BaseService) hashedResourceName(id router.InstanceID, name string, limit int) string {
	if id.InstanceName != "" {
		name += "-" + id.InstanceName
	}
	if len(name) <= limit {
		return name
	}

	h := sha256.New()
	h.Write([]byte(name))
	hash := fmt.Sprintf("%x", h.Sum(nil))
	return fmt.Sprintf("%s-%s", name[:limit-17], hash[:16])
}

func (s *BaseService) getStatusForRuntimeObject(ctx context.Context, ns string, kind string, uid types.UID) (string, error) {
	client, err := s.getClient()
	if err != nil {
		return "", err
	}
	selector := map[string]string{
		"involvedObject.kind": kind,
		"involvedObject.uid":  string(uid),
	}

	eventList, err := client.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: labels.SelectorFromSet(labels.Set(selector)).String(),
	})
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	reasonMap := map[string]bool{}
	sort.Slice(eventList.Items, func(i, j int) bool {
		return eventList.Items[i].CreationTimestamp.After(eventList.Items[j].CreationTimestamp.Time)
	})

	for _, event := range eventList.Items {
		if reasonMap[event.Reason] {
			continue
		}
		reasonMap[event.Reason] = true

		fmt.Fprintf(&buf, "%s - %s - %s\n", event.CreationTimestamp.Format(time.RFC3339), event.Type, event.Message)
	}

	return buf.String(), nil
}

func isFrozenSvc(svc *v1.Service) bool {
	if svc == nil || svc.Labels == nil {
		return false
	}
	frozen, _ := strconv.ParseBool(svc.Labels[routerFreezeLabel])
	return frozen
}
