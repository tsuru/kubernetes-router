// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "net/http"

func healthcheck(w http.ResponseWriter, req *http.Request) {
	if _, err := w.Write([]byte("WORKING")); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
	w.WriteHeader(http.StatusOK)
}
