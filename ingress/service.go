// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ingress

type IngressService interface {
	Create(appName string) error
	Remove(appName string) error
	Update(appName string) error
	Swap(appSrc, appDst string) error
	Get(appName string) (map[string]string, error)
}
