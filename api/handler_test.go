// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandler(t *testing.T) {
	tt := []struct {
		name           string
		err            error
		expectedBody   string
		expectedStatus int
	}{
		{"withoutError", nil, "", http.StatusOK},
		{"withGenericError", errors.New("internal error"), "internal error\n", http.StatusInternalServerError},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			myHandler := func(w http.ResponseWriter, r *http.Request) error {
				return tc.err
			}
			req := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
			w := httptest.NewRecorder()
			wrapped := handler(myHandler)
			wrapped.ServeHTTP(w, req)

			response := w.Result()

			if w.Body.String() != tc.expectedBody {
				t.Errorf("Expected body to be %q. Got %q", tc.expectedBody, w.Body.String())
			}
			if response.StatusCode != tc.expectedStatus {
				t.Errorf("Expected status %d. Got %d", tc.expectedStatus, response.StatusCode)
			}
		})
	}
}

func TestAuthHandler(t *testing.T) {
	h := AuthMiddleware{"user", "god"}
	tt := []struct {
		name           string
		user           string
		password       string
		expectedStatus int
	}{
		{"rightCredentials", "user", "god", http.StatusOK},
		{"wrongCredentials", "bla", "wrong", http.StatusUnauthorized},
	}
	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
			req.SetBasicAuth(tc.user, tc.password)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req, func(http.ResponseWriter, *http.Request) {
			})

			response := w.Result()

			if response.StatusCode != tc.expectedStatus {
				t.Errorf("Expected status %d. Got %d", tc.expectedStatus, response.StatusCode)
			}
		})
	}
}
