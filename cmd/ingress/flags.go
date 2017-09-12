// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// MapFlag wraps a map[string]string to be populated from
// flags with KEY=VALUE format
type MapFlag map[string]string

// String prints the json representation
func (f *MapFlag) String() string {
	repr := *f
	if repr == nil {
		repr = MapFlag{}
	}
	data, _ := json.Marshal(repr)
	return string(data)
}

// Set sets a value on the underlying map
func (f *MapFlag) Set(val string) error {
	parts := strings.SplitN(val, "=", 2)
	if *f == nil {
		*f = map[string]string{}
	}
	if len(parts) < 2 {
		return errors.New("must be on the form \"key=value\"")
	}
	(*f)[parts[0]] = parts[1]
	return nil
}
