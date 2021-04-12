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
	EnsureFn                 func(router.InstanceID, router.EnsureBackendOpts) error
	RemoveFn                 func(router.InstanceID) error
	GetAddressesFn           func(router.InstanceID) ([]string, error)
	GetStatusFn              func(router.InstanceID) (router.BackendStatus, string, error)
	GetCertificateFn         func(router.InstanceID, string) (*router.CertData, error)
	AddCertificateFn         func(router.InstanceID, string, router.CertData) error
	RemoveCertificateFn      func(router.InstanceID, string) error
	SupportedOptionsFn       func() map[string]string
	RemoveInvoked            bool
	EnsureInvoked            bool
	GetAddressesInvoked      bool
	AddCertificateInvoked    bool
	GetCertificateInvoked    bool
	RemoveCertificateInvoked bool
	SupportedOptionsInvoked  bool
	GetStatusInvoked         bool
}

// Remove calls RemoveFn
func (s *RouterMock) Remove(ctx context.Context, id router.InstanceID) error {
	s.RemoveInvoked = true
	return s.RemoveFn(id)
}

// Update calls UpdateFn
func (s *RouterMock) Ensure(ctx context.Context, id router.InstanceID, o router.EnsureBackendOpts) error {
	s.EnsureInvoked = true
	return s.EnsureFn(id, o)
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

// SupportedOptions calls SupportedOptionsFn
func (s *RouterMock) SupportedOptions(ctx context.Context) map[string]string {
	s.SupportedOptionsInvoked = true
	return s.SupportedOptionsFn()
}
