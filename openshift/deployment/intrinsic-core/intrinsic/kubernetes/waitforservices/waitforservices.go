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

// The waitforservices tool loops over the given services until the all accept
// connections.
//
// bazel run --config=intrinsic //intrinsic/kubernetes/waitforservices -- google.com:80 localhost:8080 -- /bin/echo "foo"
//
// python3 -m http.server 8080
//
// Or with a timeout:
// bazel run --config=intrinsic //intrinsic/kubernetes/waitforservices -- --timeout=15s google.com:80 localhost:8080 -- /bin/echo "foo"
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"

	"intrinsic/kubernetes/intrinsic"
	"intrinsic/kubernetes/waitforservices/checkservices"

	log "github.com/golang/glog"
)

const (
	pollInterval = 3 * time.Second
	usage        = "usage: waitforservices [service:port [...]] -- path/to/executable [args [...]]"
)

func main() {
	timeoutFlag := flag.Duration("timeout", 0, "Duration to wait before giving up (defaults to 'forever').")

	intrinsic.Init()

	if len(flag.Args()) == 0 {
		log.Exitf("Bad invocation: No args given\n%s", usage)
	}

	services, payload, err := checkservices.SplitArgs(flag.Args())
	if err != nil {
		log.Exitf("Bad invocation: %v\n%s", err, usage)
	}

	pathToExec := payload[0]
	if _, err := os.Stat(pathToExec); errors.Is(err, os.ErrNotExist) {
		log.Exitf("Bad invocation: file %q not found.", pathToExec)
	} else if err != nil {
		log.Exitf("Could not stat %q.", pathToExec)
	}

	if *timeoutFlag > 0 {
		fmt.Printf("Waiting for services with a timeout of %v: %v\n", *timeoutFlag, services)
	} else {
		fmt.Printf("Waiting for services: %v\n", services)
	}
	deadline := time.Now().Add(*timeoutFlag)
	for len(services) > 0 {
		if *timeoutFlag > 0 && time.Now().After(deadline) {
			log.Exitf("Timed out after %v waiting for the following services to start: %v\n", *timeoutFlag, services)
		}
		services = checkservices.CheckServices(services)
		time.Sleep(pollInterval)
	}
	fmt.Printf("All required services up, continuing...\n")

	if len(payload) > 0 {
		fmt.Printf("Launching %s\n", pathToExec)
		if err := syscall.Exec(pathToExec, payload, os.Environ()); err != nil {
			log.Fatal(err)
		}
	}
}
