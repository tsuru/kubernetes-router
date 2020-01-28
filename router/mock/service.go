// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mock

import "github.com/tsuru/kubernetes-router/router"

// RouterService is a router.Service mock implementation to be
// used by tests
type RouterService struct {
	CreateFn                 func(string, router.Opts) error
	RemoveFn                 func(string) error
	UpdateFn                 func(string) error
	SwapFn                   func(string, string) error
	GetFn                    func(string) (map[string]string, error)
	AddressesFn              func(string) ([]string, error)
	GetCertificateFn         func(string, string) (*router.CertData, error)
	AddCertificateFn         func(string, string, router.CertData) error
	RemoveCertificateFn      func(string, string) error
	SetCnameFn               func(appName string, cname string) error
	GetCnamesFn              func(appName string) (*router.CnamesResp, error)
	UnsetCnameFn             func(appName string, cname string) error
	SupportedOptionsFn       func() (map[string]string, error)
	CreateInvoked            bool
	RemoveInvoked            bool
	UpdateInvoked            bool
	SwapInvoked              bool
	GetInvoked               bool
	AddressesInvoked         bool
	AddCertificateInvoked    bool
	GetCertificateInvoked    bool
	RemoveCertificateInvoked bool
	GetCnamesInvoked         bool
	SetCnameInvoked          bool
	UnsetCnameInvoked        bool
	SupportedOptionsInvoked  bool
}

// Create calls CreateFn
func (s *RouterService) Create(appName string, opts router.Opts) error {
	s.CreateInvoked = true
	return s.CreateFn(appName, opts)
}

// Remove calls RemoveFn
func (s *RouterService) Remove(appName string) error {
	s.RemoveInvoked = true
	return s.RemoveFn(appName)
}

// Update calls UpdateFn
func (s *RouterService) Update(appName string) error {
	s.UpdateInvoked = true
	return s.UpdateFn(appName)
}

// Swap calls SwapFn
func (s *RouterService) Swap(appSrc string, appDst string) error {
	s.SwapInvoked = true
	return s.SwapFn(appSrc, appDst)
}

// Get calls GetFn
func (s *RouterService) Get(appName string) (map[string]string, error) {
	s.GetInvoked = true
	return s.GetFn(appName)
}

// Addresses calls AddressesFn
func (s *RouterService) Addresses(appName string) ([]string, error) {
	s.AddressesInvoked = true
	return s.AddressesFn(appName)
}

// GetCertificate calls GetCertificate
func (s *RouterService) GetCertificate(appName, certName string) (*router.CertData, error) {
	s.GetCertificateInvoked = true
	return s.GetCertificateFn(appName, certName)
}

// AddCertificate calls AddCertificate
func (s *RouterService) AddCertificate(appName string, certName string, cert router.CertData) error {
	s.AddCertificateInvoked = true
	return s.AddCertificateFn(appName, certName, cert)
}

// RemoveCertificate calls RemoveCertificate
func (s *RouterService) RemoveCertificate(appName string, certName string) error {
	s.RemoveCertificateInvoked = true
	return s.RemoveCertificateFn(appName, certName)
}

// SetCname calls SetCnameFn
func (s *RouterService) SetCname(appName string, cname string) error {
	s.SetCnameInvoked = true
	return s.SetCnameFn(appName, cname)
}

// GetCnames calls GetCnames
func (s *RouterService) GetCnames(appName string) (*router.CnamesResp, error) {
	s.GetCnamesInvoked = true
	return s.GetCnamesFn(appName)
}

// UnsetCname calls UnsetCnameFn
func (s *RouterService) UnsetCname(appName string, cname string) error {
	s.UnsetCnameInvoked = true
	return s.UnsetCnameFn(appName, cname)
}

// SupportedOptions calls SupportedOptionsFn
func (s *RouterService) SupportedOptions() (map[string]string, error) {
	s.SupportedOptionsInvoked = true
	return s.SupportedOptionsFn()
}
