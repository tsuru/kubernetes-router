// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"os"
	"time"

	"github.com/tsuru/kubernetes-router/backend"
	"github.com/tsuru/kubernetes-router/cmd"
	"github.com/tsuru/kubernetes-router/kubernetes"
	_ "github.com/tsuru/kubernetes-router/observability"
	"github.com/tsuru/kubernetes-router/router"
	"gopkg.in/yaml.v2"
)

func main() {
	listenAddr := flag.String("listen-addr", ":8077", "Listen address")
	ingressPort := flag.Int("ingress-http-port", 0, "Listen Port")
	k8sNamespace := flag.String("k8s-namespace", "tsuru", "Kubernetes namespace to create resources")
	k8sTimeout := flag.Duration("k8s-timeout", time.Second*10, "Kubernetes per-request timeout")
	k8sLabels := &cmd.MapFlag{}
	flag.Var(k8sLabels, "k8s-labels", "Labels to be added to each resource created. Expects KEY=VALUE format.")
	k8sAnnotations := &cmd.MapFlag{}
	flag.Var(k8sAnnotations, "k8s-annotations", "Annotations to be added to each resource created. Expects KEY=VALUE format.")
	runModes := cmd.StringSliceFlag{}
	flag.Var(&runModes, "controller-modes", "Defines enabled controller running modes: service, ingress, ingress-nginx or istio-gateway.")

	ingressDomain := flag.String("ingress-domain", "local", "Default domain to be used on created vhosts, local is the default. (eg: serviceName.local)")

	istioGatewaySelector := &cmd.MapFlag{}
	flag.Var(istioGatewaySelector, "istio-gateway.gateway-selector", "Gateway selector used in gateways created for apps.")

	certFile := flag.String("cert-file", "", "Path to certificate used to serve https requests")
	keyFile := flag.String("key-file", "", "Path to private key used to serve https requests")

	optsToLabels := &cmd.MapFlag{}
	flag.Var(optsToLabels, "opts-to-label", "Mapping between router options and service labels. Expects KEY=VALUE format.")

	optsToLabelsDocs := &cmd.MapFlag{}
	flag.Var(optsToLabelsDocs, "opts-to-label-doc", "Mapping between router options and user friendly help. Expects KEY=VALUE format.")

	optsToIngressAnnotations := &cmd.MapFlag{}
	flag.Var(optsToIngressAnnotations, "opts-to-ingress-annotations", "Mapping between router options and ingress annotations. Expects KEY=VALUE format.")

	optsToIngressAnnotationsDocs := &cmd.MapFlag{}
	flag.Var(optsToIngressAnnotationsDocs, "opts-to-ingress-annotations-doc", "Mapping between router options and user friendly help. Expects KEY=VALUE format.")

	ingressClass := flag.String("ingress-class", "", "Default class used for ingress objects")

	ingressAnnotationsPrefix := flag.String("ingress-annotations-prefix", "", "Default prefix for annotations based on options")

	poolLabels := &cmd.MultiMapFlag{}
	flag.Var(poolLabels, "pool-labels", "Default labels for a given pool. Expects POOL={\"LABEL\":\"VALUE\"} format.")
	clustersFilePath := flag.String("clusters-file", "", "Path to file that describes clusters, when inform this file enable the multi-cluster support")

	flag.Parse()

	err := flag.Lookup("logtostderr").Value.Set("true")
	if err != nil {
		log.Printf("failed to set log to stderr: %v\n", err)
	}

	base := &kubernetes.BaseService{
		Namespace:   *k8sNamespace,
		Timeout:     *k8sTimeout,
		Labels:      *k8sLabels,
		Annotations: *k8sAnnotations,
	}

	if len(runModes) == 0 {
		runModes = append(runModes, "service")
	}

	localBackend := &backend.LocalCluster{
		DefaultMode: runModes[0],
		Routers:     map[string]router.Router{},
	}

	for _, mode := range runModes {
		switch mode {
		case "istio-gateway":
			localBackend.Routers[mode] = &kubernetes.IstioGateway{
				BaseService:     base,
				DomainSuffix:    *ingressDomain,
				GatewaySelector: *istioGatewaySelector,
			}
		case "ingress-nginx":
			*ingressClass = "nginx"
			*ingressAnnotationsPrefix = "nginx.ingress.kubernetes.io"
			fallthrough
		case "ingress":
			localBackend.Routers[mode] = &kubernetes.IngressService{
				BaseService:           base,
				DomainSuffix:          *ingressDomain,
				OptsAsAnnotations:     *optsToIngressAnnotations,
				OptsAsAnnotationsDocs: *optsToIngressAnnotationsDocs,
				IngressClass:          *ingressClass,
				AnnotationsPrefix:     *ingressAnnotationsPrefix,
				HttpPort:              *ingressPort,
			}
		case "service", "loadbalancer":
			localBackend.Routers[mode] = &kubernetes.LBService{
				BaseService:      base,
				OptsAsLabels:     *optsToLabels,
				OptsAsLabelsDocs: *optsToLabelsDocs,
				PoolLabels:       *poolLabels,
			}
		default:
			log.Fatalf("fail parameters: Use one of the following modes: service, ingress, ingress-nginx or istio-gateway.")
		}
	}

	var routerBackend backend.Backend = localBackend
	// enable multi-cluster support when file is provided
	if *clustersFilePath != "" {
		f, err := os.Open(*clustersFilePath)
		if err != nil {
			log.Printf("failed to load clusters file: %v\n", err)
			return
		}
		clustersFile := &backend.ClustersFile{}
		err = yaml.NewDecoder(f).Decode(clustersFile)
		if err != nil {
			log.Printf("failed to load clusters file: %v\n", err)
			return
		}

		routerBackend = &backend.MultiCluster{
			Namespace:  *k8sNamespace,
			Fallback:   routerBackend,
			K8sTimeout: k8sTimeout,
			Modes:      runModes,
			Clusters:   clustersFile.Clusters,
		}
	}

	cmd.StartDaemon(cmd.DaemonOpts{
		Name:       "kubernetes-router",
		ListenAddr: *listenAddr,
		Backend:    routerBackend,
		KeyFile:    *keyFile,
		CertFile:   *certFile,
	})
}
