// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

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
	data, err := json.Marshal(repr)
	if err != nil {
		panic(err)
	}
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

// MultiMapFlag wraps a map[string]map[string]string to be populated from
// flags with KEY={K: V} format
type MultiMapFlag map[string]map[string]string

// String prints the json representation
func (f *MultiMapFlag) String() string {
	repr := *f
	if repr == nil {
		repr = MultiMapFlag{}
	}
	data, err := json.Marshal(repr)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// Set sets a value on the underlying map
func (f *MultiMapFlag) Set(val string) error {
	parts := strings.SplitN(val, "=", 2)
	if *f == nil {
		*f = map[string]map[string]string{}
	}
	if len(parts) < 2 {
		return errors.New("must be on the form \"key={\"key\": \"value\"}\"")
	}
	var innerMap map[string]string
	err := json.Unmarshal([]byte(parts[1]), &innerMap)
	if err != nil {
		return err
	}
	(*f)[parts[0]] = innerMap
	return nil
}

// StringSliceFlag wraps a string slice populated by multiple flags.
type StringSliceFlag []string

// String prints a json representation
func (f *StringSliceFlag) String() string {
	repr := *f
	if repr == nil {
		repr = StringSliceFlag{}
	}
	data, _ := json.Marshal(repr)
	return string(data)
}

// Set appends a new string to the slice
func (f *StringSliceFlag) Set(val string) error {
	*f = append(*f, val)
	return nil
}
