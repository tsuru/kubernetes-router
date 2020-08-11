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

	"github.com/stretchr/testify/assert"
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
	service.GetAddressesFn = func(id router.InstanceID) ([]string, error) {
		assert.Equal(t, "myapp", id.AppName)
		return []string{"myapp"}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, service.GetAddressesInvoked)
	var data map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &data)
	assert.NoError(t, err)
	assert.Equal(t, map[string]interface{}{
		"address":   "myapp",
		"addresses": []interface{}{"myapp"},
	}, data)
}

func TestGetBackendExplicitMode(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "xyz", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()
	service.GetAddressesFn = func(id router.InstanceID) ([]string, error) {
		assert.Equal(t, "myapp", id.AppName)
		return []string{"myapp"}, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/mymode/backend/myapp", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, service.GetAddressesInvoked)
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

	service.CreateFn = func(id router.InstanceID, opts router.Opts) error {
		if id.AppName != "myapp" {
			t.Errorf("Expected myapp. Got %s", id.AppName)
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

func TestAddBackendWithHeaderOpts(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.CreateFn = func(id router.InstanceID, opts router.Opts) error {
		assert.Equal(t, "myapp", id.AppName)
		assert.Equal(t, "mypool", opts.Pool)
		assert.Equal(t, "443", opts.ExposedPort)
		assert.Equal(t, "a.b", opts.Domain)
		expectedAdditional := map[string]string{"custom": "val", "custom2": "val2"}
		assert.Equal(t, expectedAdditional, opts.AdditionalOpts)
		return nil
	}

	reqData, _ := json.Marshal(map[string]string{"tsuru.io/app-pool": "mypool", "exposed-port": "443", "custom": "val"})
	body := bytes.NewReader(reqData)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp", body)
	req.Header.Add("X-Router-Opt", "domain=a.b")
	req.Header.Add("X-Router-Opt", "custom2=val2")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, service.CreateInvoked)
}

func TestRemoveBackend(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.RemoveFn = func(id router.InstanceID) error {
		assert.Equal(t, "myapp", id.AppName)
		return nil
	}

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

	service.UpdateFn = func(id router.InstanceID, extraData router.RoutesRequestExtraData) error {
		assert.Equal(t, "myapp", id.AppName)
		return nil
	}
	reqData := router.RoutesRequestData{}
	bodyData, err := json.Marshal(reqData)
	assert.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/backend/myapp/routes", bytes.NewReader(bodyData))
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, service.UpdateInvoked)
}

func TestSwap(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	service.SwapFn = func(src, dst router.InstanceID) error {
		assert.Equal(t, "myapp", src.AppName)
		assert.Equal(t, "otherapp", dst.AppName)
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
	service.SupportedOptionsFn = func() map[string]string {
		return map[string]string{router.ExposedPort: "", router.Domain: "Custom help."}
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

	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/backend/myapp/routes", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var data map[string][]string
	err := json.Unmarshal(w.Body.Bytes(), &data)
	assert.NoError(t, err)
	expected := map[string][]string{"addresses": nil}
	assert.Equal(t, expected, data)
}

func TestAddCertificate(t *testing.T) {
	service := &mock.RouterService{}
	api := RouterAPI{DefaultMode: "mymode", IngressServices: map[string]router.Service{"mymode": service}}
	r := api.Routes()

	certExpected := router.CertData{Certificate: "Certz", Key: "keyz"}

	service.AddCertificateFn = func(id router.InstanceID, certName string, cert router.CertData) error {
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

	service.GetCertificateFn = func(id router.InstanceID, certName string) (*router.CertData, error) {
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

	service.RemoveCertificateFn = func(id router.InstanceID, certName string) error {
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

	service.SetCnameFn = func(id router.InstanceID, cname string) error {
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
	service.GetCnamesFn = func(id router.InstanceID) (*router.CnamesResp, error) {
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

	service.UnsetCnameFn = func(id router.InstanceID, cname string) error {
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
