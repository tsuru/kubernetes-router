// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gorilla/mux"

	"github.com/tsuru/ingress-router/ingress/mock"
)

func TestHealthcheckOK(t *testing.T) {
	api := RouterAPI{}
	req := httptest.NewRequest("GET", "http://localhost", nil)
	w := httptest.NewRecorder()

	api.healthcheck(w, req)
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
	service := &mock.IngressService{}
	r := mux.NewRouter()
	api := RouterAPI{IngressService: service}
	api.Register(r)
	expected := map[string]string{"data": "myapp"}
	service.GetFn = func(name string) (map[string]string, error) {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		return expected, nil
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/backend/myapp", nil)
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

func TestAddBackend(t *testing.T) {
	service := &mock.IngressService{}
	r := mux.NewRouter()
	api := RouterAPI{IngressService: service}
	api.Register(r)

	service.CreateFn = testCalledWith("myapp", t)

	req := httptest.NewRequest(http.MethodPost, "http://localhost/backend/myapp", nil)
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
	service := &mock.IngressService{}
	r := mux.NewRouter()
	api := RouterAPI{IngressService: service}
	api.Register(r)

	service.RemoveFn = testCalledWith("myapp", t)

	req := httptest.NewRequest(http.MethodDelete, "http://localhost/backend/myapp", nil)
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
	service := &mock.IngressService{}
	r := mux.NewRouter()
	api := RouterAPI{IngressService: service}
	api.Register(r)

	service.UpdateFn = testCalledWith("myapp", t)

	req := httptest.NewRequest(http.MethodPost, "http://localhost/backend/myapp/routes", nil)
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
	service := &mock.IngressService{}
	r := mux.NewRouter()
	api := RouterAPI{IngressService: service}
	api.Register(r)

	service.SwapFn = func(app, dst string) error {
		if app != "myapp" {
			t.Errorf("Expected myapp. Got %s", app)
		}
		if dst != "otherapp" {
			t.Errorf("Expected otherapp. Got %s", dst)
		}
		return nil
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost/backend/myapp/swap?target=otherapp", nil)
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

func testCalledWith(expected string, t *testing.T) func(string) error {
	t.Helper()
	return func(name string) error {
		if name != "myapp" {
			t.Errorf("Expected myapp. Got %s", name)
		}
		return nil
	}
}
