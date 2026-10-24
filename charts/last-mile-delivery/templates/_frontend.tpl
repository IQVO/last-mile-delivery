{{/*
Fully qualified name of the frontend Module Federation remote deployment/service.

The remote (web/, `last_mile_mfe`) is served by its own nginx pod and reached
through warehouse-infra's Nginx web gateway at /mfes/last-mile-delivery/.
It is deliberately a separate workload from the API: Kong never routes to it,
and the api Service must never select it (component=frontend vs
component=api). Kept in its own helper file so the frontend component stays
self-contained (templates/frontend-*.yaml + this file + the frontend: values
block).
*/}}
{{- define "last-mile-delivery.frontendFullname" -}}
{{- include "last-mile-delivery.fullname" . }}-frontend
{{- end }}
