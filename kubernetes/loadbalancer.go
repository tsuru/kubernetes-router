// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/tsuru/kubernetes-router/router"
	"github.com/tsuru/tsuru/types/provision"
	v1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	// defaultLBPort is the default exposed port to the LB
	defaultLBPort = 80

	// ErrLoadBalancerNotReady is returned when a given LB has no IP
	ErrLoadBalancerNotReady = errors.New("load balancer is not ready")
)

// LBService manages LoadBalancer services
type LBService struct {
	*BaseService

	// OptsAsLabels maps router additional options to labels to be set on the service
	OptsAsLabels map[string]string

	// OptsAsLabelsDocs maps router additional options to user friendly help text
	OptsAsLabelsDocs map[string]string

	// PoolLabels maps router additional options for a given pool to be set on the service
	PoolLabels map[string]map[string]string
}

// Create creates a LoadBalancer type service without any selectors
func (s *LBService) Create(appName string, opts router.Opts) error {
	port, _ := strconv.Atoi(opts.ExposedPort)
	if port == 0 {
		port = defaultLBPort
	}
	app, err := s.getApp(appName)
	if err != nil {
		return err
	}
	client, err := s.getClient()
	if err != nil {
		return err
	}
	ns := s.Namespace
	if app != nil {
		ns = app.Spec.NamespaceName
	}
	targetPort := defaultServicePort
	if app != nil && app.Spec.Configs != nil {
		var process *provision.TsuruYamlKubernetesProcessConfig
		for _, group := range app.Spec.Configs.Groups {
			for procName, proc := range group {
				if procName == webProcessName {
					process = &proc
					break
				}
			}
		}
		if process == nil {
			for _, group := range app.Spec.Configs.Groups {
				for _, proc := range group {
					process = &proc
					break
				}
			}
		}
		if process != nil && len(process.Ports) > 0 {
			targetPort = process.Ports[0].TargetPort
			if targetPort == 0 {
				targetPort = process.Ports[0].Port
			}
		}
	}
	service := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(appName),
			Namespace: ns,
			Labels: map[string]string{
				appLabel:            appName,
				managedServiceLabel: "true",
				appPoolLabel:        opts.Pool,
			},
			Annotations: s.Annotations,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeLoadBalancer,
			Ports: []v1.ServicePort{{
				Name:       fmt.Sprintf("port-%d", port),
				Protocol:   v1.ProtocolTCP,
				Port:       int32(port),
				TargetPort: intstr.FromInt(targetPort),
			}},
		},
	}
	for k, v := range s.Labels {
		service.ObjectMeta.Labels[k] = v
	}
	for k, l := range s.OptsAsLabels {
		if v, ok := opts.AdditionalOpts[k]; ok {
			service.ObjectMeta.Labels[l] = v
		}
	}
	for k, l := range s.PoolLabels[opts.Pool] {
		service.ObjectMeta.Labels[k] = l
	}
	_, err = client.CoreV1().Services(ns).Create(service)
	if k8sErrors.IsAlreadyExists(err) {
		return router.ErrIngressAlreadyExists
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
	if dstApp, swapped := s.BaseService.isSwapped(service.ObjectMeta); swapped {
		return ErrAppSwapped{App: appName, DstApp: dstApp}
	}
	ns, err := s.getAppNamespace(appName)
	if err != nil {
		return err
	}
	err = client.CoreV1().Services(ns).Delete(service.Name, &metav1.DeleteOptions{})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Update updates the LoadBalancer service copying the web service
// labels, selectors, annotations and ports
func (s *LBService) Update(appName string, opts router.Opts) error {
	lbService, err := s.getLBService(appName)
	if err != nil {
		return err
	}
	if !isReady(lbService) {
		return ErrLoadBalancerNotReady
	}
	if _, isSwapped := s.isSwapped(lbService.ObjectMeta); isSwapped {
		return nil
	}
	webService, err := s.getWebService(appName)
	if err != nil {
		return err
	}
	if lbService.Labels == nil && len(webService.Labels) > 0 {
		lbService.Labels = make(map[string]string)
	}
	for k, v := range webService.Labels {
		if _, ok := s.Labels[k]; ok {
			continue
		}
		lbService.Labels[k] = v
	}
	if lbService.Annotations == nil && len(webService.Annotations) > 0 {
		lbService.Annotations = make(map[string]string)
	}
	for k, v := range webService.Annotations {
		if _, ok := s.Annotations[k]; ok {
			continue
		}
		lbService.Annotations[k] = v
	}
	lbService.Spec.Selector = webService.Spec.Selector
	client, err := s.getClient()
	if err != nil {
		return err
	}
	ns, err := s.getAppNamespace(appName)
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(ns).Update(lbService)
	return err
}

// Swap swaps the two LB services selectors
func (s *LBService) Swap(appSrc string, appDst string) error {
	srcServ, err := s.getLBService(appSrc)
	if err != nil {
		return err
	}
	if !isReady(srcServ) {
		return ErrLoadBalancerNotReady
	}
	dstServ, err := s.getLBService(appDst)
	if err != nil {
		return err
	}
	if !isReady(dstServ) {
		return ErrLoadBalancerNotReady
	}
	s.swap(srcServ, dstServ)
	client, err := s.getClient()
	if err != nil {
		return err
	}
	ns, err := s.getAppNamespace(appSrc)
	if err != nil {
		return err
	}
	ns2, err := s.getAppNamespace(appDst)
	if err != nil {
		return err
	}
	if ns != ns2 {
		return fmt.Errorf("unable to swap apps with different namespaces: %v != %v", ns, ns2)
	}
	_, err = client.CoreV1().Services(ns).Update(srcServ)
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(ns).Update(dstServ)
	if err != nil {
		s.swap(srcServ, dstServ)
		_, errRollback := client.CoreV1().Services(ns).Update(srcServ)
		if errRollback != nil {
			return fmt.Errorf("failed to rollback swap %v: %v", err, errRollback)
		}
	}
	return err
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
		ports := service.Spec.Ports
		if len(ports) != 0 {
			addr = fmt.Sprintf("%s:%d", addr, ports[0].Port)
		}
		if lbs[0].Hostname != "" {
			addr = lbs[0].Hostname
		}
	}
	return map[string]string{"address": addr}, nil
}

// SupportedOptions returns all the supported options
func (s *LBService) SupportedOptions() (map[string]string, error) {
	opts := map[string]string{
		router.ExposedPort: "",
	}
	for k, v := range s.OptsAsLabels {
		opts[k] = v
		if s.OptsAsLabelsDocs[k] != "" {
			opts[k] = s.OptsAsLabelsDocs[k]
		}
	}
	return opts, nil
}

func (s *LBService) getLBService(appName string) (*v1.Service, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	ns, err := s.getAppNamespace(appName)
	if err != nil {
		return nil, err
	}
	return client.CoreV1().Services(ns).Get(serviceName(appName), metav1.GetOptions{})
}

func (s *LBService) swap(srcServ, dstServ *v1.Service) {
	srcServ.Spec.Selector, dstServ.Spec.Selector = dstServ.Spec.Selector, srcServ.Spec.Selector
	s.BaseService.swap(&srcServ.ObjectMeta, &dstServ.ObjectMeta)
}

func serviceName(app string) string {
	return fmt.Sprintf("%s-router-lb", app)
}

func isReady(service *v1.Service) bool {
	if len(service.Status.LoadBalancer.Ingress) == 0 {
		return false
	}
	return service.Status.LoadBalancer.Ingress[0].IP != ""
}
