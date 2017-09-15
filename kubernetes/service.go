package kubernetes

import (
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

type BaseService struct {
	Namespace   string
	Timeout     time.Duration
	Client      kubernetes.Interface
	Labels      map[string]string
	Annotations map[string]string
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
