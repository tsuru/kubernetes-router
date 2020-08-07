// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package router

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalOpts(t *testing.T) {
	js := `{"tsuru.io/app-pool": "pool","exposed-port": "80","tsuru.io/teams": ["teamA", "teamB"],"custom-opt": "val"}`
	routerOpts := Opts{}
	err := json.Unmarshal([]byte(js), &routerOpts)
	assert.NoError(t, err)
	expected := Opts{Pool: "pool", ExposedPort: "80", AdditionalOpts: map[string]string{"custom-opt": "val"}}
	assert.Equal(t, expected, routerOpts)
}

func TestUnmarshalOptsWithHeaderOpts(t *testing.T) {
	js := `{"tsuru.io/app-pool": "pool","exposed-port": "80","tsuru.io/teams": ["teamA", "teamB"],"custom-opt": "val"}`
	routerOpts := Opts{
		HeaderOpts: []string{
			"x",
			"a=b",
			"b=",
			"c=a=b=c",
			"d-",
			"domain=invalid.com",
		},
	}
	err := json.Unmarshal([]byte(js), &routerOpts)
	assert.NoError(t, err)
	expected := Opts{
		Pool:        "pool",
		ExposedPort: "80",
		Domain:      "invalid.com",
		AdditionalOpts: map[string]string{
			"custom-opt": "val",
			"x":          "",
			"a":          "b",
			"b":          "",
			"c":          "a=b=c",
			"d-":         "",
		},
		HeaderOpts: []string{
			"x",
			"a=b",
			"b=",
			"c=a=b=c",
			"d-",
			"domain=invalid.com",
		},
	}
	assert.Equal(t, expected, routerOpts)
}
