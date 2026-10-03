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

// Package intrinsic provides a low-spam init function.
package intrinsic

import (

	// To avoid the name collision between intrinsic/production/intrinsic and
	// intrinsic/kubernetes/intrinsic, we manually rename this before the transformations
	// in copy.bara.sky has a chance to run.
	intrinsicinit "intrinsic/production/intrinsic"

	log "github.com/golang/glog"
)

// Init provides a safe init function for the Intrinsic runtime.
func Init() {
	intrinsicinit.Init()

	log.Info("********* Process Begin *********")
}
