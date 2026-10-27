{{/*
Expand the name of the chart.
*/}}
{{- define "last-mile-delivery.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "last-mile-delivery.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "last-mile-delivery.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "last-mile-delivery.labels" -}}
helm.sh/chart: {{ include "last-mile-delivery.chart" . }}
{{ include "last-mile-delivery.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Every Deployment and Service also pins
app.kubernetes.io/component (api), so a component added later never widens a
Service selector.
*/}}
{{- define "last-mile-delivery.selectorLabels" -}}
app.kubernetes.io/name: {{ include "last-mile-delivery.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "last-mile-delivery.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "last-mile-delivery.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "last-mile-delivery.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "last-mile-delivery.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Image reference. The release job tags the published image vX.Y.Z and packages
the chart with appVersion X.Y.Z, so an empty image.tag resolves to
v<appVersion>.
*/}}
{{- define "last-mile-delivery.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default (printf "v%s" .Chart.AppVersion) }}
{{- end }}

{{/*
MANDATORY: Postgres is the only runtime. cmd/api exits at boot without
DATABASE_URL; there is no in-memory mode. Fails the render naming the values
instead of crash-looping.
*/}}
{{- define "last-mile-delivery.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "last-mile-delivery requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: Kafka is the only runtime. cmd/api exits at boot without
KAFKA_BROKERS (outbox relay, consumers, /readyz). No default broker.
*/}}
{{- define "last-mile-delivery.requireKafka" -}}
{{- $_ := required "last-mile-delivery requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- end -}}

{{/*
The three consumers always run, each under a STABLE consumer group id that must
come from values (cmd/api exits at boot without LINEHAUL_CONSUMER_GROUP,
NODE_TOPOLOGY_CONSUMER_GROUP and SHIPPING_CONSUMER_GROUP). A stable id is what
lets a rescheduled pod resume from its committed offsets; it is never the pod
name.
*/}}
{{- define "last-mile-delivery.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "linehaulConsumerGroup" "env" "LINEHAUL_CONSUMER_GROUP") (dict "key" "nodeTopologyConsumerGroup" "env" "NODE_TOPOLOGY_CONSUMER_GROUP") (dict "key" "shippingConsumerGroup" "env" "SHIPPING_CONSUMER_GROUP") -}}
{{- $v := toString (default "" (index $.Values.config $c.key)) | trim -}}
{{- if not $v -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE, service-prefixed id, never the pod name." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back a mode switch: no *_MODE variable at all. The
service has no permissive/off/in-memory mode; it boots only on real Postgres
and Kafka.
*/}}
{{- define "last-mile-delivery.requireNoInMemoryEnv" -}}
{{- $where := .where -}}
{{- range .list -}}
{{- $name := toString .name -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "%s sets %s - *_MODE variables do not exist here: last-mile-delivery runs on real Postgres and real Kafka only, with no permissive, off or in-memory mode." $where $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not set a variable the chart renders itself for the observability
standard (the same variable would be set twice and the later one would win
silently): the api Deployment owns these four.
*/}}
{{- define "last-mile-delivery.requireCleanExtraEnv" -}}
{{- $owned := list "ENVIRONMENT" "SERVICE_VERSION" "OTEL_SERVICE_NAME" "OTEL_EXPORTER_OTLP_ENDPOINT" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- if has $name $owned -}}
{{- fail (printf "extraEnv sets %s - the chart renders it from environment / otel.* / image.tag; set the value instead." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SHUTDOWN_DRAIN_DELAY in whole seconds. The value must look like "5s" so the
grace period can be checked against it.
*/}}
{{- define "last-mile-delivery.drainSeconds" -}}
{{- $d := toString (default "5s" .Values.config.shutdownDrainDelay) -}}
{{- if not (regexMatch "^[0-9]+s$" $d) -}}
{{- fail (printf "config.shutdownDrainDelay is %q - want whole seconds such as \"5s\" (the chart adds it to the other shutdown bounds to check terminationGracePeriodSeconds)." $d) -}}
{{- end -}}
{{- trimSuffix "s" $d -}}
{{- end -}}

{{/*
The graceful shutdown in cmd/api is sequential: SHUTDOWN_DRAIN_DELAY, then the
HTTP drain (shutdownTimeout 10s), then the three consumers awaited one after
the other (workerDrainTimeout 10s each = 30s), then the outbox relay
(workerDrainTimeout 10s). Worst case drain + 50s. A shorter grace period
SIGKILLs the pod between "consumers stopped" and "relay stopped", stranding an
uncommitted offset or an in-flight outbox batch.
*/}}
{{- define "last-mile-delivery.requireGracePeriod" -}}
{{- $need := add (int (include "last-mile-delivery.drainSeconds" .)) 50 | int -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) $need -}}
{{- fail (printf "terminationGracePeriodSeconds is %v - want at least %d (config.shutdownDrainDelay + HTTP drain 10s + three consumers 3x10s + outbox relay 10s): a shorter grace period SIGKILLs the consumers or the relay mid-drain." .Values.terminationGracePeriodSeconds $need) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment includes it, so a mis-set release fails `helm template` / `helm
install`.
*/}}
{{- define "last-mile-delivery.requireRealRuntime" -}}
{{- include "last-mile-delivery.requireDatabase" . -}}
{{- include "last-mile-delivery.requireKafka" . -}}
{{- include "last-mile-delivery.requireConsumerGroups" . -}}
{{- include "last-mile-delivery.requireNoInMemoryEnv" (dict "list" .Values.extraEnv "where" "extraEnv") -}}
{{- include "last-mile-delivery.requireCleanExtraEnv" . -}}
{{- include "last-mile-delivery.requireGracePeriod" . -}}
{{- end -}}

{{/*
Fully qualified name of the read-only MCP server Deployment/Service (cmd/mcp,
ADR 0006). Component label mcp: the api Service must never select it.
*/}}
{{- define "last-mile-delivery.mcpFullname" -}}
{{- include "last-mile-delivery.fullname" . }}-mcp
{{- end }}
