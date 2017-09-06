// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tsuru/ingress-router/api"
	"github.com/tsuru/ingress-router/kubernetes"
)

func main() {
	listenAddr := flag.String("listen-addr", ":8077", "Listen address")
	k8sNamespace := flag.String("k8s-namespace", "default", "Kubernetes namespace to create ingress resources")
	flag.Parse()

	routerAPI := api.RouterAPI{
		IngressService: &kubernetes.IngressService{
			Namespace: *k8sNamespace,
		},
	}
	r := mux.NewRouter().StrictSlash(true)
	routerAPI.Register(r)

	r.Handle("/metrics", promhttp.Handler())

	r.HandleFunc("/debug/pprof/", pprof.Index)
	r.HandleFunc("/debug/pprof/heap", pprof.Index)
	r.HandleFunc("/debug/pprof/mutex", pprof.Index)
	r.HandleFunc("/debug/pprof/goroutine", pprof.Index)
	r.HandleFunc("/debug/pprof/threadcreate", pprof.Index)
	r.HandleFunc("/debug/pprof/block", pprof.Index)
	r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	r.HandleFunc("/debug/pprof/profile", pprof.Profile)
	r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	r.HandleFunc("/debug/pprof/trace", pprof.Trace)

	server := http.Server{
		Addr:         *listenAddr,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Started listening and serving at %s", *listenAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("fail serve: %v", err)
	}
}
