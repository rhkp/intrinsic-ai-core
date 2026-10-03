# Namespace-scoped ChartAssignment controller adaptation

This directory contains the pinned Cloud Robotics ChartAssignment controller
and Synk source needed for an OpenShift-specific, namespace-bounded adaptation.
The source baseline is `github.com/googlecloudrobotics/core/src` at
`v0.0.0-20260415101243-f173d97ba247`, the exact version required by the pinned
Intrinsic Core release `20260922.0`.

The upstream controller is not deployed as-is: its upstream policy binds it to
`cluster-admin`, and the controller creates chart namespaces, copies selected
Secrets, and edits default ServiceAccounts. The adaptation must remove those
behaviors, constrain ChartAssignments and release tracking to
`arhkp-intrinsic`, and fail closed on any chart resource outside that project.

The controller and Synk CRDs must be installed as `Namespaced` resources before
the controller starts. The CRD registrations themselves are cluster-level API
objects; their custom-resource instances are namespace-bound. Do not change the
scope of an established CRD in place. No CRDs or controller resources have been
installed on dev01 as part of this source preparation.

Upstream source files are Apache-2.0 licensed; see [LICENSE](LICENSE). The
specific module version is pinned in [go.mod](go.mod).
