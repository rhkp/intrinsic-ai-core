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

// Package clusterconfig exposes read-only cluster settings
package clusterconfig

import (
	"fmt"

	log "github.com/golang/glog"
	"github.com/googlecloudrobotics/core/src/go/pkg/apis/registry/v1alpha1"
)

var defaults = struct {
	canDoSim        bool
	canDoReal       bool
	hasGpu          bool
	hasExplicitACLs bool
}{
	canDoSim:        true,
	canDoReal:       true,
	hasGpu:          false,
	hasExplicitACLs: false,
}

func checkBoolWithDefault(m map[string]string, n, k string, d bool) bool {
	if val, ok := m[k]; ok {
		if val == fmt.Sprintf("%t", !d) {
			return !d
		} else if val != fmt.Sprintf("%t", d) {
			log.Errorf("Robot %q has unexpected label value for %q: %s", n, k, val)
		}
	}
	return d
}

// CanDoSim returns whether the cluster is configured to execute in simulation.
func CanDoSim(r v1alpha1.Robot) bool {
	return checkBoolWithDefault(r.Labels, r.Name, "intrinsic.ai/can_do_sim", defaults.canDoSim)
}

// CanDoReal returns whether the cluster is configured to execute on real hardware.
func CanDoReal(r v1alpha1.Robot) bool {
	return checkBoolWithDefault(r.Labels, r.Name, "intrinsic.ai/can_do_real", defaults.canDoReal)
}

// HasGpu returns whether the cluster has a GPU and the nvidia-gpu-operator.
func HasGpu(r v1alpha1.Robot) bool {
	return checkBoolWithDefault(r.Labels, r.Name, "intrinsic.ai/has_gpu", defaults.hasGpu)
}

// HasExplicitACLs returns whether the cluster was setup with acls.
// This annotation is only used for migration off implicit acls.
func HasExplicitACLs(r v1alpha1.Robot) bool {
	return checkBoolWithDefault(r.Annotations, r.Name, "intrinsic.ai/has_explicit_acls", defaults.hasExplicitACLs)
}
