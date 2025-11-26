// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package backend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/tsuru/kubernetes-router/kubernetes"
	"github.com/tsuru/kubernetes-router/observability"
	"github.com/tsuru/kubernetes-router/router"
	kubernetesGO "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

var _ Backend = &MultiCluster{}

type ClusterConfig struct {
	Name    string `json:"name"`
	Default bool   `json:"default"`
	Address string `json:"address"`
	Token   string `json:"token"`
	CA      string `json:"ca"`

	AuthProvider *clientcmdapi.AuthProviderConfig `json:"authProvider"`
	Exec         *clientcmdapi.ExecConfig         `json:"exec"`
}

type ClustersFile struct {
	Clusters []ClusterConfig `json:"clusters"`
}

type MultiCluster struct {
	Namespace  string
	Fallback   Backend
	K8sTimeout *time.Duration
	Modes      []string
	Clusters   []ClusterConfig
}

type TsuruKubeConfig struct {
	Cluster  clientcmdapi.Cluster  `json:"cluster"`
	AuthInfo clientcmdapi.AuthInfo `json:"user"`
}

func (m *MultiCluster) Router(ctx context.Context, mode string, headers http.Header) (router.Router, error) {
	timeout := time.Second * 10
	if m.K8sTimeout != nil {
		timeout = *m.K8sTimeout
	}

	var kubernetesRestConfig *rest.Config
	var err error

	name := headers.Get("X-Tsuru-Cluster-Name")
	base64KubeConfig := headers.Get("X-Tsuru-Cluster-Kube-Config")

	span := opentracing.SpanFromContext(ctx)
	if span != nil {
		span.SetTag("cluster.name", name)
	}

	if base64KubeConfig == "" {
		address := headers.Get("X-Tsuru-Cluster-Addresses")

		if address == "" {
			return m.Fallback.Router(ctx, mode, headers)
		}

		if span != nil {
			span.SetTag("cluster.address", address)
		}

		kubernetesRestConfig, err = m.getKubeConfigFromSettings(name, address, timeout)
		if err != nil {
			return nil, err
		}
	} else {
		kubernetesRestConfig, err = m.getKubeConfigFromHeader(name, base64KubeConfig, timeout)
		if err != nil {
			return nil, err
		}
	}

	k8sClient, err := kubernetesGO.NewForConfig(kubernetesRestConfig)
	if err != nil {
		return nil, err
	}

	baseService := &kubernetes.BaseService{
		Namespace:  m.Namespace,
		Timeout:    timeout,
		Client:     k8sClient,
		RestConfig: kubernetesRestConfig,
	}

	if mode == "service" || mode == "loadbalancer" || mode == "" {
		return &kubernetes.LBService{
			BaseService: baseService,
		}, nil
	}

	if mode == "ingress" {
		return &kubernetes.IngressService{
			BaseService: baseService,
		}, nil
	}

	if mode == "nginx-ingress" {
		return &kubernetes.IngressService{
			BaseService:       baseService,
			IngressClass:      "nginx",
			AnnotationsPrefix: "nginx.ingress.kubernetes.io",
			// TODO(wpjunior): may router opts plugged in here ?
		}, nil
	}

	if mode == "istio-gateway" {
		return &kubernetes.IstioGateway{
			BaseService: baseService,
		}, nil
	}

	return nil, errors.New("Mode not found")
}

func (m *MultiCluster) Healthcheck(ctx context.Context) error {
	return m.Fallback.Healthcheck(ctx)
}

func (m *MultiCluster) getKubeConfigFromHeader(name, base64KubeConfig string, timeout time.Duration) (*rest.Config, error) {
	kubeConfigData, err := base64.StdEncoding.DecodeString(base64KubeConfig)
	if err != nil {
		return nil, err
	}

	tsuruKubeConfig := TsuruKubeConfig{}
	err = json.Unmarshal(kubeConfigData, &tsuruKubeConfig)
	if err != nil {
		return nil, err
	}

	cliCfg := clientcmdapi.Config{
		APIVersion:     "v1",
		Kind:           "Config",
		CurrentContext: name,
		Clusters: map[string]*clientcmdapi.Cluster{
			name: &tsuruKubeConfig.Cluster,
		},
		Contexts: map[string]*clientcmdapi.Context{
			name: {
				Cluster:  name,
				AuthInfo: name,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			name: &tsuruKubeConfig.AuthInfo,
		},
	}

	restConfig, err := clientcmd.NewNonInteractiveClientConfig(cliCfg, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	if err != nil {
		return nil, err
	}
	restConfig.Timeout = timeout
	restConfig.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		return transport.DebugWrappers(observability.WrapTransport(rt))
	}
	return restConfig, nil
}

func (m *MultiCluster) getKubeConfigFromSettings(name, address string, timeout time.Duration) (*rest.Config, error) {
	selectedCluster := ClusterConfig{}

	for _, cluster := range m.Clusters {
		if cluster.Default {
			selectedCluster = cluster
		}
		if cluster.Name == name {
			selectedCluster = cluster
			break
		}
	}

	if selectedCluster.Name == "" {
		return nil, errors.New("cluster not found")
	}

	if selectedCluster.Address != "" {
		address = selectedCluster.Address
	}

	restConfig := &rest.Config{
		Host:        address,
		BearerToken: selectedCluster.Token,
		Timeout:     timeout,
		WrapTransport: func(rt http.RoundTripper) http.RoundTripper {
			return transport.DebugWrappers(observability.WrapTransport(rt))
		},
	}

	if selectedCluster.Exec != nil && selectedCluster.AuthProvider != nil {
		return nil, errors.New("both exec and authProvider mutually exclusive are set in the cluster config")
	}

	if selectedCluster.AuthProvider != nil {
		restConfig.AuthProvider = selectedCluster.AuthProvider
	}

	if selectedCluster.Exec != nil {
		restConfig.ExecProvider = selectedCluster.Exec
		restConfig.ExecProvider.InteractiveMode = "Never"
	}

	if selectedCluster.CA != "" {
		caData, err := base64.StdEncoding.DecodeString(selectedCluster.CA)
		if err != nil {
			return nil, err
		}
		restConfig.TLSClientConfig.CAData = caData
	}

	return restConfig, nil

}
