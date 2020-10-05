// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/tsuru/kubernetes-router/router"
	tsuruv1 "github.com/tsuru/tsuru/provision/kubernetes/pkg/apis/tsuru/v1"
	tsuruv1clientset "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
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
	headlessServiceLabel = "tsuru.io/is-headless-service"

	appBaseServiceNamespaceLabel = "router.tsuru.io/base-service-namespace"
	appBaseServiceNameLabel      = "router.tsuru.io/base-service-name"
	routerFreezeLabel            = "router.tsuru.io/freeze"

	defaultServicePort = 8888
	appLabel           = "tsuru.io/app-name"
	domainLabel        = "tsuru.io/domain-name"
	processLabel       = "tsuru.io/app-process"
	swapLabel          = "tsuru.io/swapped-with"
	appPoolLabel       = "tsuru.io/app-pool"
	webProcessName     = "web"

	appCRDName = "apps.tsuru.io"
)

// ErrNoService indicates that the app has no service running
type ErrNoService struct{ App, Process string }

func (e ErrNoService) Error() string {
	str := fmt.Sprintf("no service found for app %q", e.App)
	if e.Process != "" {
		str += fmt.Sprintf(" and process %q", e.Process)
	}
	return str
}

// ErrAppSwapped indicates when a operation cant be performed
// because the app is swapped
type ErrAppSwapped struct{ App, DstApp string }

func (e ErrAppSwapped) Error() string {
	return fmt.Sprintf("app %q currently swapped with %q", e.App, e.DstApp)
}

// BaseService has the base functionality needed by router.Service implementations
// targeting kubernetes
type BaseService struct {
	Namespace        string
	Timeout          time.Duration
	Client           kubernetes.Interface
	TsuruClient      tsuruv1clientset.Interface
	ExtensionsClient apiextensionsclientset.Interface
	Labels           map[string]string
	Annotations      map[string]string
}

// SupportedOptions returns the options supported by all services
func (k *BaseService) SupportedOptions() map[string]string {
	return nil
}

// Healthcheck uses the kubernetes client to check the connectivity
func (k *BaseService) Healthcheck() error {
	client, err := k.getClient()
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(k.Namespace).List(metav1.ListOptions{})
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
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	config.Timeout = k.Timeout
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return transport.DebugWrappers(rt)
	}
	return config, nil
}

func (k *BaseService) getWebService(appName string, extraData router.RoutesRequestExtraData, currentLabels map[string]string) (*apiv1.Service, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}

	if currentLabels != nil && extraData.Namespace == "" && extraData.Service == "" {
		extraData.Namespace = currentLabels[appBaseServiceNamespaceLabel]
		extraData.Service = currentLabels[appBaseServiceNameLabel]
	}

	if extraData.Namespace != "" && extraData.Service != "" {
		var svc *apiv1.Service
		svc, err = client.CoreV1().Services(extraData.Namespace).Get(extraData.Service, metav1.GetOptions{})
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return nil, ErrNoService{App: appName}
			}
			return nil, err
		}
		return svc, nil
	}

	namespace, err := k.getAppNamespace(appName)
	if err != nil {
		return nil, err
	}
	sel, err := makeWebSvcSelector(appName)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Services(namespace).List(metav1.ListOptions{
		LabelSelector: sel.String(),
	})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, ErrNoService{App: appName}
	}
	if len(list.Items) == 1 {
		return &list.Items[0], nil
	}
	var service *apiv1.Service
	var webSvcsCounter int
	for i := range list.Items {
		if list.Items[i].Labels[processLabel] == webProcessName {
			webSvcsCounter++
			service = &list.Items[i]
		}
	}
	if webSvcsCounter > 1 {
		log.Printf("WARNING: multiple (%d) services matching app %q and process %q", webSvcsCounter, appName, webProcessName)
		return nil, ErrNoService{App: appName, Process: webProcessName}
	}
	if service != nil {
		return service, nil
	}
	return nil, ErrNoService{App: appName, Process: webProcessName}
}

func (k *BaseService) swap(src, dst *metav1.ObjectMeta) {
	if src.Labels[swapLabel] == dst.Labels[appLabel] {
		src.Labels[swapLabel] = ""
		dst.Labels[swapLabel] = ""
	} else {
		src.Labels[swapLabel] = dst.Labels[appLabel]
		dst.Labels[swapLabel] = src.Labels[appLabel]
	}
}

func (k *BaseService) isSwapped(obj metav1.ObjectMeta) (string, bool) {
	target := obj.Labels[swapLabel]
	return target, target != ""
}

func (k *BaseService) getApp(app string) (*tsuruv1.App, error) {
	hasCRD, err := k.hasCRD()
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
	return tclient.TsuruV1().Apps(k.Namespace).Get(app, metav1.GetOptions{})
}

func (k *BaseService) getAppNamespace(appName string) (string, error) {
	app, err := k.getApp(appName)
	if err != nil {
		return "", err
	}
	if app == nil {
		return k.Namespace, nil
	}
	return app.Spec.NamespaceName, nil
}

func (k *BaseService) hasCRD() (bool, error) {
	eclient, err := k.getExtensionsClient()
	if err != nil {
		return false, err
	}
	_, err = eclient.ApiextensionsV1beta1().CustomResourceDefinitions().Get(appCRDName, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func makeWebSvcSelector(appName string) (labels.Selector, error) {
	reqs := []struct {
		key string
		op  selection.Operator
		val string
	}{
		{appLabel, selection.Equals, appName},
		{managedServiceLabel, selection.NotEquals, "true"},
		{externalServiceLabel, selection.NotEquals, "true"},
		{headlessServiceLabel, selection.NotEquals, "true"},
	}

	sel := labels.NewSelector()
	for _, reqInfo := range reqs {
		req, err := labels.NewRequirement(reqInfo.key, reqInfo.op, []string{reqInfo.val})
		if err != nil {
			return nil, err
		}
		sel = sel.Add(*req)
	}
	return sel, nil
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

func isFrozenSvc(svc *v1.Service) bool {
	if svc == nil || svc.Labels == nil {
		return false
	}
	frozen, _ := strconv.ParseBool(svc.Labels[routerFreezeLabel])
	return frozen
}
