// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/tsuru/kubernetes-router/router"
	v1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// defaultLBPort is the default exposed port to the LB
	defaultLBPort = 80

	// exposeAllPortsOpt is the flag used to expose all ports in the LB
	exposeAllPortsOpt = "expose-all-ports"

	annotationOptPrefix = "svc-annotation-"
)

var (
	// ErrLoadBalancerNotReady is returned when a given LB has no IP
	ErrLoadBalancerNotReady = errors.New("load balancer is not ready")
)

var (
	_ router.Router       = &LBService{}
	_ router.RouterStatus = &LBService{}
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

// Remove removes the LoadBalancer service
func (s *LBService) Remove(ctx context.Context, id router.InstanceID) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}
	service, err := s.getLBService(ctx, id)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if dstApp, swapped := isSwapped(service.ObjectMeta); swapped {
		return ErrAppSwapped{App: id.AppName, DstApp: dstApp}
	}
	ns, err := s.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return err
	}
	err = client.CoreV1().Services(ns).Delete(ctx, service.Name, metav1.DeleteOptions{})
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	return err
}

// Swap swaps the two LB services selectors
func (s *LBService) Swap(ctx context.Context, srcID, dstID router.InstanceID) error {
	srcServ, err := s.getLBService(ctx, srcID)
	if err != nil {
		return err
	}
	if !isReady(srcServ) {
		return ErrLoadBalancerNotReady
	}
	dstServ, err := s.getLBService(ctx, dstID)
	if err != nil {
		return err
	}
	if !isReady(dstServ) {
		return ErrLoadBalancerNotReady
	}
	if isFrozenSvc(srcServ) || isFrozenSvc(dstServ) {
		return nil
	}
	s.swap(srcServ, dstServ)
	client, err := s.getClient()
	if err != nil {
		return err
	}
	ns, err := s.getAppNamespace(ctx, srcID.AppName)
	if err != nil {
		return err
	}
	ns2, err := s.getAppNamespace(ctx, dstID.AppName)
	if err != nil {
		return err
	}
	if ns != ns2 {
		return fmt.Errorf("unable to swap apps with different namespaces: %v != %v", ns, ns2)
	}
	_, err = client.CoreV1().Services(ns).Update(ctx, srcServ, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	_, err = client.CoreV1().Services(ns).Update(ctx, dstServ, metav1.UpdateOptions{})
	if err != nil {
		s.swap(srcServ, dstServ)
		_, errRollback := client.CoreV1().Services(ns).Update(ctx, srcServ, metav1.UpdateOptions{})
		if errRollback != nil {
			return fmt.Errorf("failed to rollback swap %v: %v", err, errRollback)
		}
	}
	return err
}

// Get returns the LoadBalancer IP
func (s *LBService) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	service, err := s.getLBService(ctx, id)
	if err != nil {
		return nil, err
	}
	var addr string
	lbs := service.Status.LoadBalancer.Ingress
	if service.Annotations[externalDNSHostnameLabel] != "" {
		hostnames := strings.Split(service.Annotations[externalDNSHostnameLabel], ",")
		return hostnames, nil
	}
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
	return []string{addr}, nil
}

// SupportedOptions returns all the supported options
func (s *LBService) SupportedOptions(ctx context.Context) map[string]string {
	opts := map[string]string{
		router.ExposedPort: "",
		exposeAllPortsOpt:  "Expose all ports used by application in the Load Balancer. Defaults to false.",
	}
	for k, v := range s.OptsAsLabels {
		opts[k] = v
		if s.OptsAsLabelsDocs[k] != "" {
			opts[k] = s.OptsAsLabelsDocs[k]
		}
	}
	return opts
}

func (s *LBService) GetStatus(ctx context.Context, id router.InstanceID) (router.BackendStatus, string, error) {
	service, err := s.getLBService(ctx, id)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}
	if isReady(service) {
		return router.BackendStatusReady, "", nil
	}
	detail, err := s.getStatusForRuntimeObject(ctx, service.Namespace, "Service", service.UID)
	if err != nil {
		return router.BackendStatusNotReady, "", err
	}

	return router.BackendStatusNotReady, detail, nil
}

func (s *LBService) getLBService(ctx context.Context, id router.InstanceID) (*v1.Service, error) {
	client, err := s.getClient()
	if err != nil {
		return nil, err
	}
	ns, err := s.getAppNamespace(ctx, id.AppName)
	if err != nil {
		return nil, err
	}
	return client.CoreV1().Services(ns).Get(ctx, s.serviceName(id), metav1.GetOptions{})
}

func (s *LBService) swap(srcServ, dstServ *v1.Service) {
	srcServ.Spec.Selector, dstServ.Spec.Selector = dstServ.Spec.Selector, srcServ.Spec.Selector
	s.BaseService.swap(&srcServ.ObjectMeta, &dstServ.ObjectMeta)
}

func (s *LBService) serviceName(id router.InstanceID) string {
	return s.hashedResourceName(id, fmt.Sprintf("%s-router-lb", id.AppName), 63)
}

func isReady(service *v1.Service) bool {
	if len(service.Status.LoadBalancer.Ingress) == 0 {
		return false
	}
	// NOTE: aws load-balancers does not have IP
	return service.Status.LoadBalancer.Ingress[0].IP != "" || service.Status.LoadBalancer.Ingress[0].Hostname != ""
}

// Ensure creates or updates the LoadBalancer service copying the web service
// labels, selectors, annotations and ports

func (s *LBService) Ensure(ctx context.Context, id router.InstanceID, o router.EnsureBackendOpts) error {
	app, err := s.getApp(ctx, id.AppName)
	if err != nil {
		return err
	}
	isNew := false
	existingLBService, err := s.getLBService(ctx, id)
	var lbService *v1.Service
	if err != nil {
		if !k8sErrors.IsNotFound(err) {
			return err
		}
		isNew = true
		ns := s.Namespace
		if app != nil {
			ns = app.Spec.NamespaceName
		}
		lbService = &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.serviceName(id),
				Namespace: ns,
			},
			Spec: v1.ServiceSpec{
				Type: v1.ServiceTypeLoadBalancer,
			},
		}
	}
	if !isNew {
		lbService = existingLBService.DeepCopy()
	}
	if isFrozenSvc(lbService) {
		return nil
	}
	if _, isSwapped := isSwapped(lbService.ObjectMeta); isSwapped {
		return nil
	}

	defaultTarget, err := s.getDefaultBackendTarget(o.Prefixes)
	if err != nil {
		return err
	}

	webService, err := s.getWebService(ctx, id.AppName, *defaultTarget)
	if err != nil {
		return err
	}

	lbService.Spec.Selector = webService.Spec.Selector

	err = s.fillLabelsAndAnnotations(ctx, lbService, id, webService, o.Opts, *defaultTarget)
	if err != nil {
		return err
	}

	ports, err := s.portsForService(lbService, o.Opts, webService)
	if err != nil {
		return err
	}
	lbService.Spec.Ports = ports
	client, err := s.getClient()
	if err != nil {
		return err
	}

	if isNew {
		_, err = client.CoreV1().Services(lbService.Namespace).Create(ctx, lbService, metav1.CreateOptions{})
		return err
	}

	hasChanges := serviceHasChanges(existingLBService, lbService)

	if hasChanges {
		_, err = client.CoreV1().Services(lbService.Namespace).Update(ctx, lbService, metav1.UpdateOptions{})
		return err
	}

	return nil
}

func (s *LBService) fillLabelsAndAnnotations(ctx context.Context, svc *v1.Service, id router.InstanceID, webService *v1.Service, opts router.Opts, backendTarget router.BackendTarget) error {
	optsLabels := make(map[string]string)
	registeredOpts := s.SupportedOptions(ctx)

	optsAnnotations, err := opts.ToAnnotations()
	if err != nil {
		return err
	}
	annotations := mergeMaps(s.Annotations, optsAnnotations)

	for optName, optValue := range opts.AdditionalOpts {
		if labelName, ok := s.OptsAsLabels[optName]; ok {
			optsLabels[labelName] = optValue
			continue
		}
		if _, ok := registeredOpts[optName]; ok {
			continue
		}

		if strings.HasPrefix(optName, annotationOptPrefix) {
			// Legacy tsuru versions do not support opt names with `.`. As a
			// workaround we accept opts with the prefix `svc-annotation-` to
			// use `:` instead of `.`.
			optName = strings.TrimPrefix(optName, annotationOptPrefix)
			optName = strings.ReplaceAll(optName, ":", ".")
		}

		if strings.HasSuffix(optName, "-") {
			delete(annotations, strings.TrimSuffix(optName, "-"))
		} else {
			annotations[optName] = optValue
		}
	}

	vhost := ""
	if len(opts.Domain) > 0 {
		vhost = opts.Domain
	} else if opts.DomainSuffix != "" {
		if opts.DomainPrefix == "" {
			vhost = fmt.Sprintf("%v.%v", id.AppName, opts.DomainSuffix)
		} else {
			vhost = fmt.Sprintf("%v.%v.%v", opts.DomainPrefix, id.AppName, opts.DomainSuffix)
		}
	}
	if vhost != "" {
		annotations[externalDNSHostnameLabel] = vhost
	}

	labels := []map[string]string{
		svc.Labels,
		s.PoolLabels[opts.Pool],
		optsLabels,
		s.Labels,
		{
			appLabel:             id.AppName,
			managedServiceLabel:  "true",
			externalServiceLabel: "true",
			appPoolLabel:         opts.Pool,
		},
	}

	if webService != nil {
		labels = append(labels, webService.Labels)
		annotations = mergeMaps(annotations, webService.Annotations)
	}

	labels = append(labels, map[string]string{
		appBaseServiceNamespaceLabel: backendTarget.Namespace,
		appBaseServiceNameLabel:      backendTarget.Service,
	})

	svc.Labels = mergeMaps(labels...)
	svc.Annotations = annotations
	return nil
}

func (s *LBService) portsForService(svc *v1.Service, opts router.Opts, baseSvc *v1.Service) ([]v1.ServicePort, error) {
	additionalPort, _ := strconv.Atoi(opts.ExposedPort)
	if additionalPort == 0 {
		additionalPort = defaultLBPort
	}

	existingPorts := map[int32]*v1.ServicePort{}
	for i, port := range svc.Spec.Ports {
		existingPorts[port.Port] = &svc.Spec.Ports[i]
	}

	exposeAllPorts, _ := strconv.ParseBool(opts.AdditionalOpts[exposeAllPortsOpt])

	var basePorts, wantedPorts []v1.ServicePort
	if baseSvc != nil {
		basePorts = baseSvc.Spec.Ports
	}

	for _, basePort := range basePorts {
		if len(wantedPorts) == 0 {
			var name string
			if basePort.Name != "" {
				name = fmt.Sprintf("%s-extra", basePort.Name)
			} else {
				name = fmt.Sprintf("port-%d", additionalPort)
			}
			wantedPorts = append(wantedPorts, v1.ServicePort{
				Name:       name,
				Protocol:   basePort.Protocol,
				Port:       int32(additionalPort),
				TargetPort: basePort.TargetPort,
			})
		}
		if !exposeAllPorts {
			break
		}

		if basePort.Port == int32(additionalPort) {
			// Skipping ports conflicting with additional port
			continue
		}
		basePort.NodePort = 0
		wantedPorts = append(wantedPorts, basePort)
	}

	if len(wantedPorts) == 0 {
		wantedPorts = append(wantedPorts, v1.ServicePort{
			Name:       fmt.Sprintf("port-%d", additionalPort),
			Protocol:   v1.ProtocolTCP,
			Port:       int32(additionalPort),
			TargetPort: intstr.FromInt(defaultServicePort),
		})
	}

	for i := range wantedPorts {
		existingPort, ok := existingPorts[wantedPorts[i].Port]
		if ok {
			wantedPorts[i].NodePort = existingPort.NodePort
		}
	}

	return wantedPorts, nil
}

func serviceHasChanges(existing *v1.Service, svc *v1.Service) (hasChanges bool) {
	if !reflect.DeepEqual(existing.Spec, svc.Spec) {
		return true
	}
	for key, value := range svc.Annotations {
		if existing.Annotations[key] != value {
			return true
		}
	}
	for key, value := range svc.Labels {
		if existing.Labels[key] != value {
			return true
		}
	}
	return false
}

func mergeMaps(entries ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, entry := range entries {
		for k, v := range entry {
			if _, isSet := result[k]; !isSet {
				result[k] = v
			}
		}
	}
	return result
}
