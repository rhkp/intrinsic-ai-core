# Copyright 2026 Intrinsic Innovation LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

{{/*
Generates SecurityPolicy with defaultAction: Allow for public bypass routes.

Parameters:
  - route: (required) Target HTTPRoute/GRPCRoute resource name.
  - context: (required) Root Helm template context ($).
  - kind: (optional, default "HTTPRoute") Target route kind ("HTTPRoute" or "GRPCRoute").
  - app: (optional, default .route) Label app: <app>.

Usage:
  {{ include "auth.bypass" (dict "route" "frontend-bypass" "context" $) }}
  {{ include "auth.bypass" (dict "route" "frontend-bypass" "kind" "GRPCRoute" "context" $) }}
*/}}
{{- define "auth.bypass" -}}
{{- $kind := default "HTTPRoute" .kind -}}
{{- $appLabel := default .route .app -}}

---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: {{ .route }}-{{ $kind | lower }}-bypass
  labels:
    app.kubernetes.io/name: {{ .context.Chart.Name }}
    app: {{ $appLabel }}
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: {{ $kind }}
      name: {{ .route }}
  authorization:
    defaultAction: Allow
{{- end -}}
