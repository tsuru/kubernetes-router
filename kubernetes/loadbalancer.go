package kubernetes

import (
	"fmt"

	"github.com/tsuru/ingress-router/ingress"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/pkg/api/v1"
)

// LBService manages LoadBalancer services
type LBService struct {
	*BaseService
}

// Create creates a LoadBalancer type service without any selectors
func (s *LBService) Create(appName string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        serviceName(appName),
			Namespace:   s.Namespace,
			Labels:      map[string]string{appLabel: appName},
			Annotations: s.Annotations,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeLoadBalancer,
			Ports: []v1.ServicePort{
				{
					Protocol:   "TCP",
					Port:       int32(defaultServicePort),
					TargetPort: intstr.FromInt(defaultServicePort),
				},
			},
		},
	}
	for k, v := range s.Labels {
		service.ObjectMeta.Labels[k] = v
	}
	_, err = client.CoreV1().Services(s.Namespace).Create(service)
	if k8sErrors.IsAlreadyExists(err) {
		return ingress.ErrIngressAlreadyExists
	}
	return err
}

// Remove removes the LoadBalancer service
func (s *LBService) Remove(appName string) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}
	service, err := s.getLBService(appName)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if dstApp, ok := service.Labels[swapLabel]; ok {
		return ErrAppSwapped{App: appName, DstApp: dstApp}
	}
	err = client.CoreV1().Services(s.Namespace).Delete(serviceName(appName), &metav1.DeleteOptions{})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Update updates the LoadBalancer service copying the web service
// labels, selectors, annotations and ports
func (s *LBService) Update(appName string) error {
	webService, err := s.getWebService(appName)
	if err != nil {
		return err
	}
	lbService, err := s.getLBService(appName)
	if err != nil {
		return err
	}
	for k, v := range webService.Labels {
		if _, ok := s.Labels[k]; ok {
			continue
		}
		lbService.Labels[k] = v
	}
	for k, v := range webService.Annotations {
		if _, ok := s.Annotations[k]; ok {
			continue
		}
		lbService.Annotations[k] = v
	}
	lbService.Spec.Selector = webService.Spec.Selector
	for i, p := range webService.Spec.Ports {
		lbService.Spec.Ports[i].Port = p.Port
		lbService.Spec.Ports[i].Protocol = p.Protocol
		lbService.Spec.Ports[i].TargetPort = p.TargetPort
	}
	client, err := s.getClient()
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(s.Namespace).Update(lbService)
	return err
}

func (s *LBService) Swap(appSrc string, appDst string) error {
	panic("not implemented")
}

// Get returns the LoadBalancer IP
func (s *LBService) Get(appName string) (map[string]string, error) {
	service, err := s.getLBService(appName)
	if err != nil {
		return nil, err
	}
	var addr string
	lbs := service.Status.LoadBalancer.Ingress
	if len(lbs) != 0 {
		addr = lbs[0].IP
	}
	return map[string]string{"address": addr}, nil
}

func (s *LBService) getLBService(appName string) (*v1.Service, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	return client.CoreV1().Services(s.Namespace).Get(serviceName(appName), metav1.GetOptions{})
}

func serviceName(app string) string {
	return fmt.Sprintf("%s-router-lb", app)
}
