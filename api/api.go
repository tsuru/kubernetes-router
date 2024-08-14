// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/mux"
	"github.com/tsuru/kubernetes-router/backend"
	"github.com/tsuru/kubernetes-router/router"
	"golang.org/x/sync/errgroup"
)

var httpSchemeRegex = regexp.MustCompile(`^https?://`)

const checkPathTimeout = 2 * time.Second

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
	r.Handle("/backend/{name}", handler(a.ensureBackend)).Methods(http.MethodPut)
	r.Handle("/backend/{name}", handler(a.removeBackend)).Methods(http.MethodDelete)
	r.Handle("/backend/{name}/status", handler(a.status)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/routes", handler(a.getRoutes)).Methods(http.MethodGet)
	r.Handle("/info", handler(a.info)).Methods(http.MethodGet)

	// TLS
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.addCertificate)).Methods(http.MethodPut)
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.getCertificate)).Methods(http.MethodGet)
	r.Handle("/backend/{name}/certificate/{certname}", handler(a.removeCertificate)).Methods(http.MethodDelete)

	// cert-manager
	r.Handle("/backend/{name}/cert-manager/{certname}", handler(a.issueCertManagerCert)).Methods(http.MethodPut)

	// Supports
	r.Handle("/support/tls", handler(a.supportTLS)).Methods(http.MethodGet)
	r.Handle("/support/info", handler(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})).Methods(http.MethodGet)
	r.Handle("/support/status", handler(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})).Methods(http.MethodGet)
	r.Handle("/support/prefix", handler(func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})).Methods(http.MethodGet)
	r.Handle("/support/v2", handler(func(w http.ResponseWriter, r *http.Request) error {
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

type statusResp struct {
	Status router.BackendStatus `json:"status"`
	Detail string               `json:"detail"`
	Checks []urlCheck           `json:"checks,omitempty"`
}

type urlCheck struct {
	Address string `json:"address"`
	Status  int    `json:"status"`
	Error   string `json:"error"`
}

// status returns backend events
func (a *RouterAPI) status(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}

	rsp := statusResp{
		Status: router.BackendStatusReady,
	}

	grp, ctx := errgroup.WithContext(ctx)

	grp.Go(func() error {
		checks, checkErr := checkPath(ctx, r.URL.Query().Get("checkpath"), svc, instanceID(r))
		if checkErr != nil {
			return checkErr
		}
		rsp.Checks = checks
		return nil
	})

	grp.Go(func() error {
		statusRouter, ok := svc.(router.RouterStatus)
		if !ok {
			return nil
		}
		status, detail, statusErr := statusRouter.GetStatus(ctx, instanceID(r))
		if statusErr != nil {
			return statusErr
		}
		rsp.Status = status
		rsp.Detail = detail
		return nil
	})

	err = grp.Wait()
	if err != nil {
		return err
	}

	return json.NewEncoder(w).Encode(rsp)
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
func (a *RouterAPI) ensureBackend(w http.ResponseWriter, r *http.Request) error {
	vars := mux.Vars(r)
	ctx := r.Context()

	opts := &router.EnsureBackendOpts{
		Opts: router.Opts{
			HeaderOpts: r.Header.Values("X-Router-Opt"),
		},
	}
	err := json.NewDecoder(r.Body).Decode(opts)
	if err != nil {
		return err
	}

	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}

	return svc.Ensure(ctx, instanceID(r), *opts)
}

// getRoutes always returns an empty address list to force tsuru to call
// addRoutes on every routes rebuild call.
func (a *RouterAPI) getRoutes(w http.ResponseWriter, r *http.Request) error {
	type resp struct {
		Addresses []string `json:"addresses"`
	}
	return json.NewEncoder(w).Encode(resp{})
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

// issueCertManagerCert Issues certificate for the app
func (a *RouterAPI) issueCertManagerCert(_ http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	vars := mux.Vars(r)
	name := vars["name"]
	certName := vars["certname"]

	log.Printf("Issuing certificate %s for %s", certName, name)

	cert := router.CertManagerIssuerData{}
	err := json.NewDecoder(r.Body).Decode(&cert)
	if err != nil {
		return err
	}

	svc, err := a.router(ctx, vars["mode"], r.Header)
	if err != nil {
		return err
	}

	cmRouter, ok := svc.(router.RouterCertManager)
	if !ok {
		return httpError{
			Status: http.StatusNotFound,
			Body:   fmt.Sprintf("Router %s doesn't have cert-manager support", vars["mode"]),
		}
	}

	return cmRouter.IssueCertManagerCertificate(ctx, instanceID(r), certName, cert.Issuer)
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

func checkPath(ctx context.Context, path string, svc router.Router, instance router.InstanceID) ([]urlCheck, error) {
	if path == "" {
		return nil, nil
	}

	addrs, err := svc.GetAddresses(ctx, instance)
	if err != nil {
		return nil, err
	}

	wg := sync.WaitGroup{}
	checks := make(chan urlCheck, len(addrs))
	for _, addr := range addrs {
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			check := urlCheck{
				Address: addr,
			}

			url := fmt.Sprintf("%s/%s", strings.TrimSuffix(addr, "/"), strings.TrimPrefix(path, "/"))
			if !httpSchemeRegex.MatchString(url) {
				url = "http://" + url
			}

			ctxWithTimeout, cancel := context.WithTimeout(ctx, checkPathTimeout)
			defer cancel()
			req, err := http.NewRequestWithContext(ctxWithTimeout, http.MethodGet, url, nil)
			if err != nil {
				check.Error = err.Error()
				checks <- check
				return
			}
			rsp, err := http.DefaultClient.Do(req)
			if err != nil {
				check.Error = err.Error()
				checks <- check
				return
			}
			check.Status = rsp.StatusCode
			checks <- check
		}(addr)
	}

	wg.Wait()
	close(checks)

	var ret []urlCheck
	for check := range checks {
		ret = append(ret, check)
	}
	return ret, nil
}
