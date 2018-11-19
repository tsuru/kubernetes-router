// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"log"
	"net/http"

	"github.com/tsuru/kubernetes-router/router"
)

type httpError struct {
	Body   string
	Status int
}

func (h httpError) Error() string {
	return h.Body
}

type handler func(http.ResponseWriter, *http.Request) error

// ServeHTTP serves an HTTP request
func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handleError(h(w, r), w, r)
}

func handleError(err error, w http.ResponseWriter, r *http.Request) {
	if err != nil {
		log.Printf("error during request %v %v: %v", r.Method, r.URL.Path, err)
		if httpErr, ok := err.(httpError); ok {
			http.Error(w, httpErr.Error(), httpErr.Status)
			return
		}
		if err == router.ErrIngressAlreadyExists {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// AuthMiddleware is an http.Handler with Basic Auth
type AuthMiddleware struct {
	User string
	Pass string
}

// ServeHTTP serves an HTTP request with Basic Auth
func (h AuthMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if h.User == "" && h.Pass == "" {
		next(w, r)
		return
	}
	rUser, rPass, _ := r.BasicAuth()

	if rUser != h.User || rPass != h.Pass {
		w.Header().Set("WWW-Authenticate", "Basic realm=\"Authorization Required\"")
		http.Error(w, "Not Authorized", http.StatusUnauthorized)
		return
	}
	next(w, r)
}
