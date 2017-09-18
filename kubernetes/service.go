// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	apiv1 "k8s.io/client-go/pkg/api/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

const (
	defaultServicePort = 8888
	appLabel           = "tsuru.io/app-name"
	processLabel       = "tsuru.io/app-process"
	swapLabel          = "tsuru.io/swapped-with"
	appPoolLabel       = "tsuru.io/app-pool"
	poolLabel          = "tsuru.io/pool"
	webProcessName     = "web"
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
	Namespace   string
	Timeout     time.Duration
	Client      kubernetes.Interface
	Labels      map[string]string
	Annotations map[string]string
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
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	config.Timeout = k.Timeout
	config.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return transport.DebugWrappers(rt)
	}
	return kubernetes.NewForConfig(config)
}

func (k *BaseService) getWebService(appName string) (*apiv1.Service, error) {
	client, err := k.getClient()
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Services(k.Namespace).List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s!=%s", appLabel, appName, managedServiceLabel, "true"),
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
		delete(src.Labels, swapLabel)
		delete(dst.Labels, swapLabel)
	} else {
		src.Labels[swapLabel] = dst.Labels[appLabel]
		dst.Labels[swapLabel] = src.Labels[appLabel]
	}
}

func (k *BaseService) isSwapped(obj metav1.ObjectMeta) (string, bool) {
	target, swapped := obj.Labels[swapLabel]
	return target, swapped
}
