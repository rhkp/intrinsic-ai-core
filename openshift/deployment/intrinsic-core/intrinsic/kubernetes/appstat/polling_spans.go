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

// Package pollingspans provides a tracker that can be used to create traces
// (e.g. OpenCensus) based on point-in-time polling events.
package pollingspans

// SpanLike is satisfied by OpenCensus' trace.Span.
type SpanLike interface {
	End()
}

type eventSpan struct {
	span  SpanLike
	alive bool
}

// Tracker contains the information about currently active spans.
type Tracker struct {
	spans map[string]*eventSpan
}

// New constructs a [Tracker] that is ready to use.
func New() *Tracker {
	return &Tracker{
		spans: make(map[string]*eventSpan),
	}
}

// BeforeRegistration MUST be called before you start registering events with
// [RegisterEvent].
func (t *Tracker) BeforeRegistration() {
	for _, es := range t.spans {
		es.alive = false
	}
}

// RegisterEvent checks if the [Tracker] already tracks "name" and bumps its
// liveliness accordingly. Otherwise, if "name" is not known to the [Tracker],
// it creates a span using the given "ctor" function.
func (t *Tracker) RegisterEvent(name string, ctor func() SpanLike) {
	if _, ok := t.spans[name]; !ok {
		t.spans[name] = &eventSpan{ctor(), true}
	} else {
		t.spans[name].alive = true
	}
}

// AfterRegistration MUST be called after you've registered all events with
// [RegisterEvent].
func (t *Tracker) AfterRegistration() {
	for name, es := range t.spans {
		if !es.alive {
			es.span.End()
			delete(t.spans, name)
		}
	}
}

// End finalizes all open traces. You MUST call it after the polling is done.
func (t *Tracker) End() {
	for name, es := range t.spans {
		es.span.End()
		delete(t.spans, name)
	}
}
