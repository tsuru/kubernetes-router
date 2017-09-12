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
	f.Set("a=1")
	f.Set("b=2")
	f.Set("c=3")
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
