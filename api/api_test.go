// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tsuru/kubernetes-router/router"
	"github.com/tsuru/kubernetes-router/router/mock"
)

func TestHealthcheckOK(t *testing.T) {
	api := RouterAPI{}
	req := httptest.NewRequest("GET", "http://localhost", nil)
	w := httptest.NewRecorder()

	api.Healthcheck(w, req)

	resp := w.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	if string(body) != "WORKING" {
		t.Errorf("Expected body \"WORKING\". Got %q", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
}

func TestGetBackend(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	expected := map[string]string{"data": "myapp"}
	service.GetFn = func(name string) (map[string]string, error) {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		return expected, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.GetInvoked {
		t.Errorf("Service Get function not invoked")
	}
	var data map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &data)
	if err != nil {
		t.Errorf("Failed to unmarshal: %v", err)
	}
	if !reflect.DeepEqual(data, expected) {
		t.Errorf("Expected %v. Got %v", expected, data)
	}
}

func TestGetBackendExplicitMode(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "xyz", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	expected := map[string]string{"data": "myapp"}
	service.GetFn = func(name string) (map[string]string, error) {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		return expected, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/mymode/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.GetInvoked {
		t.Errorf("Service Get function not invoked")
	}
}

func TestGetBackendInvalidMode(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/othermode/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status %q. Got %q", http.StatusNotFound, resp.Status)
	}
}

func TestAddBackend(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.CreateFn = func(name string, opts router.Opts) error {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		if opts.Pool != "mypool" {
			t.Errorf("Expected mypool. Got %v.", opts.Pool)
		}
		if opts.ExposedPort != "443" {
			t.Errorf("Expected 443. Got %v.", opts.ExposedPort)
		}
		expectedAdditional := map[string]string{"custom": "val"}
		if !reflect.DeepEqual(opts.AdditionalOpts, expectedAdditional) {
			t.Errorf("Expect %v. Got %v", expectedAdditional, opts.AdditionalOpts)
		}
		return nil
	}

	reqData, _ := json.Marshal(map[string]string{"tsuru.io/app-pool": "mypool", "exposed-port": "443", "custom": "val"})
	body := bytes.NewReader(reqData)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.CreateInvoked {
		t.Errorf("Service Create function not invoked")
	}
}

func TestRemoveBackend(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.RemoveFn = testCalledWith("myapp", t)

	req := httptest.NewRequest(http.MethodDelete, "http://localhost/api/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.RemoveInvoked {
		t.Errorf("Service Remove function not invoked")
	}
}

func TestAddRoutes(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.UpdateFn = func(name string, opts router.Opts) error {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp/routes", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.UpdateInvoked {
		t.Errorf("Service Update function not invoked")
	}
}

func TestSwap(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.SwapFn = func(app, dst string) error {
		if app != "myapp" {
			t.Errorf("Expected myapp. Got %s", app)
		}
		if dst != "otherapp" {
			t.Errorf("Expected otherapp. Got %s", dst)
		}
		return nil
	}

	data, _ := json.Marshal(map[string]string{"Target": "otherapp"})
	body := bytes.NewReader(data)

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp/swap", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.SwapInvoked {
		t.Errorf("Service Swap function not invoked")
	}
}

func TestInfo(t *testing.T) {
	service := &mock.RouterService{}
	service.SupportedOptionsFn = func() (map[string]string, error) {
		return map[string]string{router.ExposedPort: "", router.Domain: "Custom help."}, nil
	}

	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/info", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}

	var info map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &info)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v", err)
	}
	expected := map[string]string{
		"exposed-port": "Port to be exposed by the Load Balancer. Defaults to 80.",
		"domain":       "Custom help.",
	}
	if !reflect.DeepEqual(info, expected) {
		t.Errorf("Expected %v. Got %v", expected, info)
	}
}

func TestGetRoutes(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.AddressesFn = func(app string) ([]string, error) {
		return []string{"localhost:8080"}, nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp/routes", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.AddressesInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
	var data map[string][]string
	err := json.Unmarshal(w.Body.Bytes(), &data)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v", err)
	}
	expected := map[string][]string{"addresses": {"localhost:8080"}}
	if !reflect.DeepEqual(data, expected) {
		t.Errorf("Expected %v. Got %v", expected, data)
	}
}

func TestAddCertificate(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	certExpected := router.CertData{Certificate: "Certz", Key: "keyz"}

	service.AddCertificateFn = func(appName string, certName string, cert router.CertData) error {
		if !reflect.DeepEqual(certExpected, cert) {
			t.Errorf("Expected %v. Got %v", certExpected, cert)
		}
		return nil
	}

	reqData, _ := json.Marshal(certExpected)
	body := bytes.NewReader(reqData)

	req := httptest.NewRequest(http.MethodPut, "http://localhost/api/backend/myapp/certificate/certname", body)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.AddCertificateInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
}

func TestGetCertificate(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.GetCertificateFn = func(appName, certName string) (*router.CertData, error) {
		cert := router.CertData{Certificate: "Certz", Key: "keyz"}
		return &cert, nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp/certificate/certname", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.GetCertificateInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
	var data router.CertData
	err := json.Unmarshal(w.Body.Bytes(), &data)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v", err)
	}
	expected := router.CertData{Certificate: "Certz", Key: "keyz"}
	if !reflect.DeepEqual(data, expected) {
		t.Errorf("Expected %v. Got %v", expected, data)
	}
}

func TestRemoveCertificate(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.RemoveCertificateFn = func(appName, certName string) error {
		return nil
	}

	req := httptest.NewRequest(http.MethodDelete, "http://localhost/api/backend/myapp/certificate/certname", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.RemoveCertificateInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
}

func TestSetCname(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	cnameExpected := "cname1"

	service.SetCnameFn = func(appName string, cname string) error {
		if !reflect.DeepEqual(cname, cnameExpected) {
			t.Errorf("Expected %v. Got %v", cnameExpected, cname)
		}
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp/cname/cname1", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.SetCnameInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
}

func TestGetCnames(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	cnames := router.CnamesResp{
		Cnames: []string{
			"cname1",
			"cname2",
		},
	}
	service.GetCnamesFn = func(appName string) (*router.CnamesResp, error) {
		return &cnames, nil
	}

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp/cname", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.GetCnamesInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
	var data router.CnamesResp
	err := json.Unmarshal(w.Body.Bytes(), &data)
	if err != nil {
		t.Errorf("Expected err to be nil. Got %v", err)
	}
	if !reflect.DeepEqual(data, cnames) {
		t.Errorf("Expected %v. Got %v", cnames, data)
	}
}

func TestUnsetCname(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	cnameExpected := "cname1"

	service.UnsetCnameFn = func(appName string, cname string) error {
		if !reflect.DeepEqual(cname, cnameExpected) {
			t.Errorf("Expected %v. Got %v", cnameExpected, cname)
		}
		return nil
	}

	req := httptest.NewRequest(http.MethodDelete, "http://localhost/api/backend/myapp/cname/cname1", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
	if !service.UnsetCnameInvoked {
		t.Errorf("Service Addresses function not invoked")
	}
}

func testCalledWith(expected string, t *testing.T) func(string) error {
	t.Helper()
	return func(name string) error {
		if name != expected {
			t.Errorf("Expected %s. Got %s", expected, name)
		}
		return nil
	}
}
