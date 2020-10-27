// Copyright 2017 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mock

import (
	"context"

	"github.com/tsuru/kubernetes-router/router"
)

var _ router.Router = &RouterMock{}

// RouterMock is a router.Router mock implementation to be
// used by tests
type RouterMock struct {
	CreateFn                 func(router.InstanceID, router.Opts) error
	RemoveFn                 func(router.InstanceID) error
	UpdateFn                 func(router.InstanceID, router.RoutesRequestExtraData) error
	SwapFn                   func(router.InstanceID, router.InstanceID) error
	GetAddressesFn           func(router.InstanceID) ([]string, error)
	GetStatusFn              func(router.InstanceID) (router.BackendStatus, string, error)
	GetCertificateFn         func(router.InstanceID, string) (*router.CertData, error)
	AddCertificateFn         func(router.InstanceID, string, router.CertData) error
	RemoveCertificateFn      func(router.InstanceID, string) error
	SetCnameFn               func(id router.InstanceID, cname string) error
	GetCnamesFn              func(id router.InstanceID) (*router.CnamesResp, error)
	UnsetCnameFn             func(id router.InstanceID, cname string) error
	SupportedOptionsFn       func() map[string]string
	CreateInvoked            bool
	RemoveInvoked            bool
	UpdateInvoked            bool
	SwapInvoked              bool
	GetAddressesInvoked      bool
	AddCertificateInvoked    bool
	GetCertificateInvoked    bool
	RemoveCertificateInvoked bool
	GetCnamesInvoked         bool
	SetCnameInvoked          bool
	UnsetCnameInvoked        bool
	SupportedOptionsInvoked  bool
	GetStatusInvoked         bool
}

// Create calls CreateFn
func (s *RouterMock) Create(ctx context.Context, id router.InstanceID, opts router.Opts) error {
	s.CreateInvoked = true
	return s.CreateFn(id, opts)
}

// Remove calls RemoveFn
func (s *RouterMock) Remove(ctx context.Context, id router.InstanceID) error {
	s.RemoveInvoked = true
	return s.RemoveFn(id)
}

// Update calls UpdateFn
func (s *RouterMock) Update(ctx context.Context, id router.InstanceID, extraData router.RoutesRequestExtraData) error {
	s.UpdateInvoked = true
	return s.UpdateFn(id, extraData)
}

// Swap calls SwapFn
func (s *RouterMock) Swap(ctx context.Context, appSrc, appDst router.InstanceID) error {
	s.SwapInvoked = true
	return s.SwapFn(appSrc, appDst)
}

// Get calls GetFn
func (s *RouterMock) GetAddresses(ctx context.Context, id router.InstanceID) ([]string, error) {
	s.GetAddressesInvoked = true
	return s.GetAddressesFn(id)
}

func (s *RouterMock) GetStatus(ctx context.Context, id router.InstanceID) (router.BackendStatus, string, error) {
	s.GetStatusInvoked = true
	return s.GetStatusFn(id)
}

// GetCertificate calls GetCertificate
func (s *RouterMock) GetCertificate(ctx context.Context, id router.InstanceID, certName string) (*router.CertData, error) {
	s.GetCertificateInvoked = true
	return s.GetCertificateFn(id, certName)
}

// AddCertificate calls AddCertificate
func (s *RouterMock) AddCertificate(ctx context.Context, id router.InstanceID, certName string, cert router.CertData) error {
	s.AddCertificateInvoked = true
	return s.AddCertificateFn(id, certName, cert)
}

// RemoveCertificate calls RemoveCertificate
func (s *RouterMock) RemoveCertificate(ctx context.Context, id router.InstanceID, certName string) error {
	s.RemoveCertificateInvoked = true
	return s.RemoveCertificateFn(id, certName)
}

// SetCname calls SetCnameFn
func (s *RouterMock) SetCname(ctx context.Context, id router.InstanceID, cname string) error {
	s.SetCnameInvoked = true
	return s.SetCnameFn(id, cname)
}

// GetCnames calls GetCnames
func (s *RouterMock) GetCnames(ctx context.Context, id router.InstanceID) (*router.CnamesResp, error) {
	s.GetCnamesInvoked = true
	return s.GetCnamesFn(id)
}

// UnsetCname calls UnsetCnameFn
func (s *RouterMock) UnsetCname(ctx context.Context, id router.InstanceID, cname string) error {
	s.UnsetCnameInvoked = true
	return s.UnsetCnameFn(id, cname)
}

// SupportedOptions calls SupportedOptionsFn
func (s *RouterMock) SupportedOptions(ctx context.Context) map[string]string {
	s.SupportedOptionsInvoked = true
	return s.SupportedOptionsFn()
}
