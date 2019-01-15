// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"
	"net/http"
	"time"

	tsuruv1clientset "github.com/tsuru/tsuru/provision/kubernetes/pkg/client/clientset/versioned"
	apiv1 "k8s.io/api/core/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

const (
	// managedServiceLabel is added to every service created by the router
	managedServiceLabel = "tsuru.io/router-lb"

	headlessServiceLabel = "tsuru.io/is-headless-service"

	defaultServicePort = 8888
	appLabel           = "tsuru.io/app-name"
	domainLabel        = "tsuru.io/domain-name"
	processLabel       = "tsuru.io/app-process"
	swapLabel          = "tsuru.io/swapped-with"
	appPoolLabel       = "tsuru.io/app-pool"
	poolLabel          = "tsuru.io/pool"
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

// Addresses return the addresses of every node on the same pool as the
// app Service pool
func (k *BaseService) Addresses(appName string) ([]string, error) {
	service, err := k.getWebService(appName)
	if err != nil {
		return nil, err
	}
	var port int32
	if len(service.Spec.Ports) > 0 {
		port = service.Spec.Ports[0].NodePort
	}
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	pool := service.Labels[appPoolLabel]
	nodes, err := client.CoreV1().Nodes().List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", poolLabel, pool),
	})
	if err != nil {
		return nil, err
	}
	addresses := make([]string, len(nodes.Items))
	for i := range nodes.Items {
		addr := nodes.Items[i].Name
		for _, a := range nodes.Items[i].Status.Addresses {
			if a.Type == apiv1.NodeInternalIP {
				addr = a.Address
				break
			}
		}
		addresses[i] = fmt.Sprintf("http://%s:%d", addr, port)
	}
	return addresses, nil
}

// SupportedOptions returns the options supported by all services
func (k *BaseService) SupportedOptions() (map[string]string, error) {
	return nil, nil
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

func (k *BaseService) getWebService(appName string) (*apiv1.Service, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	namespace, err := k.getAppNamespace(appName)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Services(namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s!=%s,%s!=%s", appLabel, appName, managedServiceLabel, "true", headlessServiceLabel, "true"),
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
	for i := range list.Items {
		if list.Items[i].Labels[processLabel] == webProcessName {
			return &list.Items[i], nil
		}
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

func (k *BaseService) getAppNamespace(app string) (string, error) {
	hasCRD, err := k.hasCRD()
	if err != nil {
		return "", err
	}
	if !hasCRD {
		return k.Namespace, nil
	}
	tclient, err := k.getTsuruClient()
	if err != nil {
		return "", err
	}
	appCR, err := tclient.TsuruV1().Apps(k.Namespace).Get(app, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return appCR.Spec.NamespaceName, nil
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
