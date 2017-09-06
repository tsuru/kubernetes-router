// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mock

// IngressService is a ingress.Service mock implementation to be
// used by tests
type IngressService struct {
	CreateFn         func(string) error
	RemoveFn         func(string) error
	UpdateFn         func(string) error
	SwapFn           func(string, string) error
	GetFn            func(string) (map[string]string, error)
	AddressesFn      func(string) ([]string, error)
	CreateInvoked    bool
	RemoveInvoked    bool
	UpdateInvoked    bool
	SwapInvoked      bool
	GetInvoked       bool
	AddressesInvoked bool
}

// Create calls CreateFn
func (s *IngressService) Create(appName string) error {
	s.CreateInvoked = true
	return s.CreateFn(appName)
}

// Remove calls RemoveFn
func (s *IngressService) Remove(appName string) error {
	s.RemoveInvoked = true
	return s.RemoveFn(appName)
}

// Update calls UpdateFn
func (s *IngressService) Update(appName string) error {
	s.UpdateInvoked = true
	return s.UpdateFn(appName)
}

// Swap calls SwapFn
func (s *IngressService) Swap(appSrc string, appDst string) error {
	s.SwapInvoked = true
	return s.SwapFn(appSrc, appDst)
}

// Get calls GetFn
func (s *IngressService) Get(appName string) (map[string]string, error) {
	s.GetInvoked = true
	return s.GetFn(appName)
}

// Addresses calls AddressesFn
func (s *IngressService) Addresses(appName string) ([]string, error) {
	s.AddressesInvoked = true
	return s.AddressesFn(appName)
}
