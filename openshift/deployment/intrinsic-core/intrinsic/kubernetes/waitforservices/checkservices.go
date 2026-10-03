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

// Package checkservices contains helper functions for the waitforservices binary.
package checkservices

import (
	"fmt"
	"net"
	"slices"
	"time"
)

// CheckServices loops over all host:port strings and tests if there is a
// service listening. It returns a list of services that it was not able to
// connect to.
func CheckServices(toCheck []string) []string {
	tryAgain := toCheck[:0]
	for _, s := range toCheck {
		// TODO(ensonic): make 'tcp' part of the server spec to support tcp4/tcp6?
		c, err := net.DialTimeout("tcp", s, 1*time.Second)
		if err != nil {
			fmt.Printf("Failed to connect to %s : %v\n", s, err)
			tryAgain = append(tryAgain, s)
		} else {
			fmt.Printf("%s is up\n", s)
			c.Close()
		}
	}
	return tryAgain
}

// SplitArgs looks for an arg containing '--' and returns two slices of the args before and after.
func SplitArgs(args []string) ([]string, []string, error) {
	if i := slices.Index(args, "--"); i != -1 {
		return args[:i], args[i+1:], nil
	}
	return nil, nil, fmt.Errorf("Missing '--' divider in args list")
}
