// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/tsuru/kubernetes-router/router"
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
	// TLS
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.addCertificate)).Methods(http.MethodPut)
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.getCertificate)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.removeCertificate)).Methods(http.MethodDelete)
	// CNAME
	r.Handle("/backend/{name}/cname/{cname}", handler(a.setCname)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/cname", handler(a.getCnames)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/cname/{cname}", handler(a.unsetCname)).Methods(http.MethodDelete)
	// Supports
	r.Handle("/support/tls", handler(a.supportTLS)).Methods(http.MethodGet)
	r.Handle("/support/cname", handler(a.supportCNAME)).Methods(http.MethodGet)
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
	routerOpts := router.Opts{}
	err := json.NewDecoder(r.Body).Decode(&routerOpts)
	if err != nil {
		return err
	}
	if len(routerOpts.Domain) > 0 && len(routerOpts.Route) == 0 {
		routerOpts.Route = "/"
	}
	return a.IngressService.Create(name, routerOpts)
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
	return a.IngressService.Update(name, router.Opts{})
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

// addCertificate Add certificate to app
func (a *RouterAPI) addCertificate(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Adding on %s certificate %s", name, certName)
	cert := router.CertData{}
	err := json.NewDecoder(r.Body).Decode(&cert)
	if err != nil {
		return err
	}
	return a.IngressService.(router.ServiceTLS).AddCertificate(name, certName, cert)
}

// getCertificate Return certificate for app
func (a *RouterAPI) getCertificate(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Getting certificate %s from %s", certName, name)
	cert, err := a.IngressService.(router.ServiceTLS).GetCertificate(name, certName)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return err
	}
	b, err := json.Marshal(&cert)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// removeCertificate Delete certificate for app
func (a *RouterAPI) removeCertificate(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Removing certificate %s from %s", certName, name)
	err := a.IngressService.(router.ServiceTLS).RemoveCertificate(name, certName)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
	}
	return err
}

// setCname Add CNAME to app
func (a *RouterAPI) setCname(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	cname := vars["cname"]
	log.Printf("Adding on %s CNAME %s", name, cname)
	err := a.IngressService.(router.ServiceCNAME).SetCname(name, cname)
	if err != nil {
		if strings.Contains(err.Error(), "exists") {
			w.WriteHeader(http.StatusConflict)
		}
		w.WriteHeader(http.StatusNotFound)
	}
	return err
}

// getCnames Return CNAMEs for app
func (a *RouterAPI) getCnames(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	log.Printf("Getting CNAMEs from %s", name)
	cnames, err := a.IngressService.(router.ServiceCNAME).GetCnames(name)
	if err != nil {
		return err
	}
	b, err := json.Marshal(&cnames)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// unsetCname Delete CNAME for app
func (a *RouterAPI) unsetCname(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	name := vars["name"]
	cname := vars["cname"]
	log.Printf("Removing CNAME %s from %s", cname, name)
	return a.IngressService.(router.ServiceCNAME).UnsetCname(name, cname)
}

// Check for TLS Support
func (a *RouterAPI) supportTLS(w http.ResponseWriter, r *http.Request) error {
	var err error
	_, ok := a.IngressService.(router.ServiceTLS)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, err = w.Write([]byte(fmt.Sprintf("No TLS Capabilities")))
		return err
	}
	_, err = w.Write([]byte("OK"))
	return err
}

// Check for CNAME Support
func (a *RouterAPI) supportCNAME(w http.ResponseWriter, r *http.Request) error {
	var err error
	_, ok := a.IngressService.(router.ServiceCNAME)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, err = w.Write([]byte(fmt.Sprintf("No CNAME Capabilities")))
		return err
	}
	_, err = w.Write([]byte("OK"))
	return err
}
