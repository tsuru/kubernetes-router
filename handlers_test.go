// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthcheckOK(t *testing.T) {
	req := httptest.NewRequest("GET", "http://localhost", nil)
	w := httptest.NewRecorder()

	healthcheck(w, req)
	resp := w.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	if string(body) != "WORKING" {
		t.Errorf("Expected body \"WORKING\". Got %q", string(body))
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %q. Got %q", http.StatusOK, resp.Status)
	}
}
