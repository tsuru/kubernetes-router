// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/tsuru/ingress-router/router"
)

// RouterAPI implements Tsuru HTTP router API
type RouterAPI struct {
	IngressService router.Service
}

// Register registers RouterAPI routes
func (a *RouterAPI) Register(r *mux.Router) {
	r.Handle("/backend/{name}", handler(a.getBackend)).Methods(http.MethodGet)
	r.Handle("/backend/{name}", handler(a.addBackend)).Methods(http.MethodPost)
	r.Handle("/backend/{name}", handler(a.updateBackend)).Methods(http.MethodPut)
	r.Handle("/backend/{name}", handler(a.removeBackend)).Methods(http.MethodDelete)
	r.Handle("/backend/{name}/routes", handler(a.getRoutes)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/routes", handler(a.addRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/routes/remove", handler(a.removeRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/swap", handler(a.swap)).Methods(http.MethodPost)
	r.Handle("/healthcheck", handler(a.healthcheck)).Methods(http.MethodGet)
}

// getBackend returns the address for the load balancer registered in
// the ingress by a ingress controller
func (a *RouterAPI) getBackend(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	info, err := a.IngressService.Get(name)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(info)
}

// addBackend creates a Ingress for a given app configuration pointing
// to a non existent service
func (a *RouterAPI) addBackend(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	return a.IngressService.Create(name)
}

// updateBackend is no-op
func (a *RouterAPI) updateBackend(w http.ResponseWriter, r *http.Request) error {
	return nil
}

// removeBackend removes the Ingress for a given app
func (a *RouterAPI) removeBackend(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	return a.IngressService.Remove(name)
}

// addRoutes updates the Ingress to point to the correct service
func (a *RouterAPI) addRoutes(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	return a.IngressService.Update(name)
}

// removeRoutes is no-op
func (a *RouterAPI) removeRoutes(w http.ResponseWriter, r *http.Request) error {
	return nil
}

func (a *RouterAPI) getRoutes(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	endpoints, err := a.IngressService.Addresses(name)
	if err != nil {
		return err
	}
	type resp struct {
		Addresses []string `json:"addresses"`
	}
	response := resp{Addresses: endpoints}
	return json.NewEncoder(w).Encode(response)
}

func (a *RouterAPI) swap(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	target := r.FormValue("target")
	if target == "" {
		return errors.New("invalid target")
	}
	return a.IngressService.Swap(name, target)
}

func (a *RouterAPI) healthcheck(w http.ResponseWriter, req *http.Request) error {
	if hc, ok := a.IngressService.(router.HealthcheckableService); ok {
		if err := hc.Healthcheck(); err != nil {
			return fmt.Errorf("failed to check IngressService: %v", err)
		}
	}
	_, err := w.Write([]byte("WORKING"))
	return err
}
