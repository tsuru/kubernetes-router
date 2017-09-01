// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/urfave/negroni"
)

func main() {
	listenAddr := flag.String("listen-addr", ":8077", "Listen address")
	flag.Parse()

	r := mux.NewRouter().StrictSlash(true)
	n := negroni.New(negroni.NewRecovery(), negroni.NewLogger())
	r.Handle("/metrics", promhttp.Handler())
	r.HandleFunc("/healthcheck", healthcheck)
	n.UseHandler(r)

	server := http.Server{
		Addr:         *listenAddr,
		Handler:      n,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	log.Printf("Starting listen and server at %s", *listenAddr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("fail serve: %v", err)
	}
}
