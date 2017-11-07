// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnmarshalOpts(t *testing.T) {
	js := `{"tsuru.io/app-pool": "pool","exposed-port": "80","tsuru.io/teams": ["teamA", "teamB"],"custom-opt": "val"}`
	routerOpts := Opts{}
	err := json.Unmarshal([]byte(js), &routerOpts)
	if err != nil {
		t.Fatalf("Expected nil error. Got %v", err)
	}
	expected := Opts{Pool: "pool", ExposedPort: "80", AdditionalOpts: map[string]string{"custom-opt": "val"}}
	if !reflect.DeepEqual(routerOpts, expected) {
		t.Fatalf("Expected %v. Got %v", expected, routerOpts)
	}
}
