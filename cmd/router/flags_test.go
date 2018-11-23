// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMapFlag(t *testing.T) {
	var f MapFlag
	err := f.Set("a=1")
	if err != nil {
		t.Fatalf("Expected nil. Got %v.", err)
	}
	err = f.Set("b=2")
	if err != nil {
		t.Fatalf("Expected nil. Got %v.", err)
	}
	err = f.Set("c=3")
	if err != nil {
		t.Fatalf("Expected nil. Got %v.", err)
	}
	expected := MapFlag{"a": "1", "b": "2", "c": "3"}
	if !reflect.DeepEqual(f, expected) {
		t.Fatalf("Expected %v. Got %v.", expected, f)
	}
}

func TestMapFlagInvalid(t *testing.T) {
	var f MapFlag
	err := f.Set("a")
	if err == nil {
		t.Fatal("Expected err. Got nil")
	}
}

func TestMultiMapFlag(t *testing.T) {
	var f MultiMapFlag
	err := f.Set("a={\"v\": \"1\"}")
	if err != nil {
		t.Fatalf("Expected nil. Got %v.", err)
	}
	err = f.Set("b={\"v\": \"2\", \"x\":\"3\"}")
	if err != nil {
		t.Fatalf("Expected nil. Got %v.", err)
	}
	expected := MultiMapFlag{"a": {"v": "1"}, "b": {"v": "2", "x": "3"}}
	if !reflect.DeepEqual(f, expected) {
		t.Fatalf("Expected %v. Got %v.", expected, f)
	}
}

func TestStringSliceFlag(t *testing.T) {
	var f StringSliceFlag
	err := f.Set("a")
	require.NoError(t, err)
	err = f.Set("b")
	require.NoError(t, err)
	err = f.Set("c")
	require.NoError(t, err)
	expected := StringSliceFlag{
		"a", "b", "c",
	}
	if !reflect.DeepEqual(f, expected) {
		t.Fatalf("Expected %v. Got %v.", expected, f)
	}
}
