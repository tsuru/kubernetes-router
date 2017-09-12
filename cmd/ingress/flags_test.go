// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"reflect"
	"testing"
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
