// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/tsuru/kubernetes-router/router"
)

const (
	poolRouterOpts = "tsuru.io/app-pool"
)

// RouterAPI implements Tsuru HTTP router API
type RouterAPI struct {
	IngressService router.Service
}

// Routes returns an mux for the API routes
func (a *RouterAPI) Routes() *mux.Router {
	r := mux.NewRouter().PathPrefix("/api").Subrouter()
	r.Handle("/backend/{name}", handler(a.getBackend)).Methods(http.MethodGet)
	r.Handle("/backend/{name}", handler(a.addBackend)).Methods(http.MethodPost)
	r.Handle("/backend/{name}", handler(a.updateBackend)).Methods(http.MethodPut)
	r.Handle("/backend/{name}", handler(a.removeBackend)).Methods(http.MethodDelete)
	r.Handle("/backend/{name}/routes", handler(a.getRoutes)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/routes", handler(a.addRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/routes/remove", handler(a.removeRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/swap", handler(a.swap)).Methods(http.MethodPost)
	return r
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
	routerOpts := make(map[string]interface{})
	err := json.NewDecoder(r.Body).Decode(&routerOpts)
	if err != nil {
		return err
	}
	labels := make(map[string]string)
	if l, ok := routerOpts[poolRouterOpts]; ok {
		labels[poolRouterOpts], ok = l.(string)
		if !ok {
			return fmt.Errorf("invalid router option %q: %v", poolRouterOpts, labels[poolRouterOpts])
		}
	}
	return a.IngressService.Create(name, labels)
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
	type swapReq struct {
		Target string
	}
	var req swapReq
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return errors.New("error parsing request")
	}
	if req.Target == "" {
		return httpError{Body: "empty target", Status: http.StatusBadRequest}
	}
	return a.IngressService.Swap(name, req.Target)
}

// Healthcheck checks the health of the service
func (a *RouterAPI) Healthcheck(w http.ResponseWriter, req *http.Request) {
	var err error
	defer func() {
		if err != nil {
			glog.Errorf("failed to write healthcheck: %v", err)
		}
	}()
	if hc, ok := a.IngressService.(router.HealthcheckableService); ok {
		if err = hc.Healthcheck(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err = w.Write([]byte(fmt.Sprintf("failed to check IngressService: %v", err)))
			return
		}
	}
	_, err = w.Write([]byte("WORKING"))
}
