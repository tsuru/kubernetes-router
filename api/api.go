// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/tsuru/kubernetes-router/backend"
	"github.com/tsuru/kubernetes-router/router"
)

// RouterAPI implements Tsuru HTTP router API
type RouterAPI struct {
	Backend backend.Backend
}

// Routes returns an mux for the API routes
func (a *RouterAPI) Routes() *mux.Router {
	r := mux.NewRouter()
	a.registerRoutes(r.PathPrefix("/api").Subrouter())
	a.registerRoutes(r.PathPrefix("/api/{mode}").Subrouter())
	return r
}

func (a *RouterAPI) registerRoutes(r *mux.Router) {
	r.Handle("/backend/{name}", handler(a.getBackend)).Methods(http.MethodGet)
	r.Handle("/backend/{name}", handler(a.addBackend)).Methods(http.MethodPost)
	r.Handle("/backend/{name}", handler(a.updateBackend)).Methods(http.MethodPut)
	r.Handle("/backend/{name}", handler(a.removeBackend)).Methods(http.MethodDelete)
	r.Handle("/backend/{name}/routes", handler(a.getRoutes)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/routes", handler(a.addRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/routes/remove", handler(a.removeRoutes)).Methods(http.MethodPost)
	r.Handle("/backend/{name}/swap", handler(a.swap)).Methods(http.MethodPost)

	r.Handle("/info", handler(a.info)).Methods(http.MethodGet)

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
	r.Handle("/support/info", handler(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})).Methods(http.MethodGet)
	r.Handle("/support/prefix", handler(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})).Methods(http.MethodGet)
}

func (a *RouterAPI) router(ctx context.Context, mode string, header http.Header) (router.Router, error) {
	router, err := a.Backend.Router(ctx, mode, header)
	if err == backend.ErrBackendNotFound {
		return nil, httpError{Status: http.StatusNotFound}
	}
	return router, nil
}

func instanceID(r *http.Request) router.InstanceID {
	vars := mux.Vars(r)
	return router.InstanceID{
		AppName:      vars["name"],
		InstanceName: r.Header.Get("X-Router-Instance"),
	}
}

// getBackend returns the address for the load balancer registered in
// the ingress by a ingress controller
func (a *RouterAPI) getBackend(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	addrs, err := svc.GetAddresses(ctx, instanceID(r))
	if err != nil {
		return err
	}
	type getBackendResponse struct {
		Address   string   `json:"address"`
		Addresses []string `json:"addresses"`
	}
	rsp := getBackendResponse{
		Addresses: addrs,
	}
	if len(addrs) > 0 {
		rsp.Address = addrs[0]
	}
	return json.NewEncoder(w).Encode(rsp)
}

// addBackend creates a Ingress for a given app configuration pointing
// to a non existent service
func (a *RouterAPI) addBackend(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	routerOpts := router.Opts{
		HeaderOpts: r.Header.Values("X-Router-Opt"),
	}
	err := json.NewDecoder(r.Body).Decode(&routerOpts)
	if err != nil {
		return err
	}
	if len(routerOpts.Domain) > 0 && len(routerOpts.Route) == 0 {
		routerOpts.Route = "/"
	}
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	return svc.Create(ctx, instanceID(r), routerOpts)
}

// updateBackend is no-op
func (a *RouterAPI) updateBackend(w http.ResponseWriter, r *http.Request) error {
	return nil
}

// removeBackend removes the Ingress for a given app
func (a *RouterAPI) removeBackend(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	return svc.Remove(ctx, instanceID(r))
}

// addRoutes updates the Ingress to point to the correct service
func (a *RouterAPI) addRoutes(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	ctx := r.Context()

	var routesData router.RoutesRequestData
	err := json.NewDecoder(r.Body).Decode(&routesData)
	if err != nil {
		return err
	}
	if routesData.Prefix != "" {
		// Do nothing for all prefixes, except the default one.
		return nil
	}

	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}

	return svc.Update(ctx, instanceID(r), routesData.ExtraData)
}

// removeRoutes is no-op
func (a *RouterAPI) removeRoutes(w http.ResponseWriter, r *http.Request) error {
	return nil
}

// getRoutes always returns an empty address list to force tsuru to call
// addRoutes on every routes rebuild call.
func (a *RouterAPI) getRoutes(w http.ResponseWriter, r *http.Request) error {
	type resp struct {
		Addresses []string `json:"addresses"`
	}
	return json.NewEncoder(w).Encode(resp{})
}

func (a *RouterAPI) swap(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
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
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	src := instanceID(r)
	dst := router.InstanceID{
		InstanceName: src.InstanceName,
		AppName:      req.Target,
	}
	return svc.Swap(ctx, src, dst)
}

func (a *RouterAPI) info(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	opts := svc.SupportedOptions(ctx)
	allOpts := router.DescribedOptions()
	info := make(map[string]string)
	for k, v := range opts {
		vv := v
		if vv == "" {
			vv = allOpts[k]
		}
		info[k] = vv
	}
	return json.NewEncoder(w).Encode(info)
}

// Healthcheck checks the health of the service
func (a *RouterAPI) Healthcheck(w http.ResponseWriter, r *http.Request) {
	err := a.Backend.Healthcheck(r.Context())
	if err != nil {
		glog.Errorf("failed to write healthcheck: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, err.Error())
		return
	}

	fmt.Fprint(w, "WORKING")
}

// addCertificate Add certificate to app
func (a *RouterAPI) addCertificate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Adding on %s certificate %s", name, certName)
	cert := router.CertData{}
	err := json.NewDecoder(r.Body).Decode(&cert)
	if err != nil {
		return err
	}
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	return svc.(router.RouterTLS).AddCertificate(ctx, instanceID(r), certName, cert)
}

// getCertificate Return certificate for app
func (a *RouterAPI) getCertificate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Getting certificate %s from %s", certName, name)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	cert, err := svc.(router.RouterTLS).GetCertificate(ctx, instanceID(r), certName)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return err
	}
	b, err := json.Marshal(&cert)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(b)
	return err
}

// removeCertificate Delete certificate for app
func (a *RouterAPI) removeCertificate(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]
	log.Printf("Removing certificate %s from %s", certName, name)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	err = svc.(router.RouterTLS).RemoveCertificate(ctx, instanceID(r), certName)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
	}
	return err
}

// setCname Add CNAME to app
func (a *RouterAPI) setCname(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	cname := vars["cname"]
	log.Printf("Adding on %s CNAME %s", name, cname)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	err = svc.(router.RouterCNAME).SetCname(ctx, instanceID(r), cname)
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
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	log.Printf("Getting CNAMEs from %s", name)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	cnames, err := svc.(router.RouterCNAME).GetCnames(ctx, instanceID(r))
	if err != nil {
		return err
	}
	b, err := json.Marshal(&cnames)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(b)
	return err
}

// unsetCname Delete CNAME for app
func (a *RouterAPI) unsetCname(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	cname := vars["cname"]
	log.Printf("Removing CNAME %s from %s", cname, name)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}
	return svc.(router.RouterCNAME).UnsetCname(ctx, instanceID(r), cname)
}

// Check for TLS Support
func (a *RouterAPI) supportTLS(w http.ResponseWriter, r *http.Request) error {
	var err error
	vars := mux.Vars(r)
	svc, err := a.router(r.Context(), vars["mode"], r.Header)
	if err != nil {
		return err
	}
	_, ok := svc.(router.RouterTLS)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, err = w.Write([]byte("No TLS Capabilities"))
		return err
	}
	_, err = w.Write([]byte("OK"))
	return err
}

// Check for CNAME Support
func (a *RouterAPI) supportCNAME(w http.ResponseWriter, r *http.Request) error {
	var err error
	vars := mux.Vars(r)
	svc, err := a.router(r.Context(), vars["mode"], r.Header)
	if err != nil {
		return err
	}
	_, ok := svc.(router.RouterCNAME)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, err = w.Write([]byte("No CNAME Capabilities"))
		return err
	}
	_, err = w.Write([]byte("OK"))
	return err
}
