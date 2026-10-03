// Copyright 2026 Intrinsic Innovation LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package privileges validation for privileges requested by services.
package privileges

import (
	"fmt"
	"regexp"

	"github.com/pkg/errors"

	rsdpb "intrinsic/resources/proto/resource_service_definition_go_proto"
	rtrpb "intrinsic/resources/proto/resource_type_runtime_go_proto"
)

var (
	// errSecurityContext occurs when the user attempts to use a security context
	// that is not allowed on a multi-tenant project.
	errSecurityContext = errors.New("security context not allowed")
	// errHostPath occurs when the user attempts to use a security context
	// that is not allowed on a multi-tenant project.
	errHostPath = errors.New("host path not allowed")
)

// limitedSecurityContexts returns a function that checks a resource spec.  It
// returns an error if any image in the spec requires a privileged container or
// capabilities not explicitly specified in allowedCapabilities.
func limitedSecurityContexts(allowedCapabilities []string) func(*rsdpb.ResourceSpec) error {
	allowlist := make(map[string]struct{})
	for _, c := range allowedCapabilities {
		allowlist[c] = struct{}{}
	}
	return func(rs *rsdpb.ResourceSpec) error {
		for _, i := range rs.GetImage() {
			// Disallow images with insecure security context settings.
			if i.GetSecurityContext().GetPrivileged() {
				return fmt.Errorf("%w: attempt to enable privileged", errSecurityContext)
			}
			for _, c := range i.GetSecurityContext().GetCapabilities().GetAdd() {
				if _, ok := allowlist[c]; !ok {
					return fmt.Errorf("%w: attempt to add capability %q", errSecurityContext, c)
				}
			}
		}
		return nil
	}
}

// limitedHostPaths returns a function that checks a resource spec.
//
// It returns an error if any requested host path does not match one of the allowed patterns.
func limitedHostPaths(allowedHostPathPatterns []*regexp.Regexp) func(*rsdpb.ResourceSpec) error {
	return func(rs *rsdpb.ResourceSpec) error {
		for _, v := range rs.GetVolumes() {
			if v.GetHostPath() != nil && !matchesAnyPattern(v.GetHostPath().GetPath(), allowedHostPathPatterns) {
				return fmt.Errorf("%w: attempt to mount host path %q", errHostPath, v.GetHostPath().GetPath())
			}
		}
		return nil
	}
}

// simAndReal lifts a resource spec check to a service definition by applying
// the same check twice to both the sim and real spec.
func simAndReal(check func(*rsdpb.ResourceSpec) error) func(*rsdpb.ResourceServiceDefinition) error {
	return func(sd *rsdpb.ResourceServiceDefinition) error {
		if err := check(sd.GetSimSpec()); err != nil {
			return fmt.Errorf("sim spec: %w", err)
		}
		if err := check(sd.GetRealSpec()); err != nil {
			return fmt.Errorf("real spec: %w", err)
		}
		return nil
	}
}

func matchesAnyPattern(path string, patterns []*regexp.Regexp) bool {
	for _, p := range patterns {
		if p.MatchString(path) {
			return true
		}
	}
	return false
}

func (v *multiCheckValidator) Validate(rt *rtrpb.ResourceTypeRuntime) error {
	for _, check := range v.checks {
		if err := check(rt.GetServiceDef()); err != nil {
			return err
		}
	}
	return nil
}

type multiCheckValidator struct {
	checks []func(*rsdpb.ResourceServiceDefinition) error
}

// Validator provides an interface to check resource types against
// current cluster settings.
type Validator interface {
	// Validate checks for expanded privileges used by resources.
	Validate(rt *rtrpb.ResourceTypeRuntime) error
}

// ValidateOptions specify how validation should be performed
type ValidateOptions struct {
	// EnableSecurityContext allows for an expanded set of fields in the
	// security context of the image to be specified.
	EnableSecurityContext bool
	// EnableRelaxedHostPathChecks allows for an expanded set of host paths to
	// be specified for volume mounts for pods.
	EnableRelaxedHostPathChecks bool
}

// NewValidator returns a validator given the provided options.
func NewValidator(opts ValidateOptions) Validator {
	var checks []func(*rsdpb.ResourceServiceDefinition) error
	if !opts.EnableSecurityContext {
		// Enforce that assets use unprivileged security contexts and are
		// limited in the capabilities they can request for multi-tenant
		// projects.  Currently the list is those required by cameras and ICON.
		checks = append(checks, simAndReal(limitedSecurityContexts([]string{
			"IPC_LOCK",
			"SYS_NICE",
			"SYS_RAWIO",
		})))
	}
	if !opts.EnableRelaxedHostPathChecks {
		checks = append(checks, simAndReal(limitedHostPaths([]*regexp.Regexp{
			// keep-sorted start
			regexp.MustCompile("^/tmp/intrinsic_icon$"),
			regexp.MustCompile("^/tmp/service_volumes/.+"),
			regexp.MustCompile("^/var/lib/file-storage/service_volumes/.+"),
			regexp.MustCompile("^/var/lib/intrinsic_icon_traces$"),
			// keep-sorted end
		})))
	}
	return &multiCheckValidator{checks}
}
