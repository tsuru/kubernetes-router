// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import "net/http"

type handler func(http.ResponseWriter, *http.Request) error

// ServeHTTP serves an HTTP request
func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	handleError(h(w, r), w, r)
}

func handleError(err error, w http.ResponseWriter, r *http.Request) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
