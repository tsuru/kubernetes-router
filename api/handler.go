// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"net/http"

	"github.com/tsuru/kubernetes-router/router"
)

type handler func(http.ResponseWriter, *http.Request) error

// ServeHTTP serves an HTTP request
func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handleError(h(w, r), w, r)
}

func handleError(err error, w http.ResponseWriter, r *http.Request) {
	if err != nil {
		if err == router.ErrIngressAlreadyExists {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type AuthMiddleware struct {
	User string
	Pass string
}

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
