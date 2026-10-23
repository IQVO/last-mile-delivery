#!/usr/bin/env bash
# Chart render assertions for last-mile-delivery: Postgres and Kafka are the
# only runtime, so the mandatory configuration has no silent default, no *_MODE
# is ever rendered, the env cmd/api reads is the env the chart renders, and the
# pod is hardened with room to shut down.
# Run: bash charts/last-mile-delivery/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=last-mile-delivery
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers and the three consumer groups. None is ever dialled.
groups=(linehaulConsumerGroup:last-mile-delivery-linehaul
  nodeTopologyConsumerGroup:last-mile-delivery-node-topology
  shippingConsumerGroup:last-mile-delivery-shipping)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done
# Every optional value set, so every env var the binary can read is rendered.
everything=(--set config.outboxRelayInterval=2s --set config.outboxMaxAttempts=5
  --set config.shutdownDrainDelay=7s --set config.logLevel=debug --set config.httpAddr=:9090
  --set autoscaling.api.enabled=true --set ingress.enabled=true --set gatewayApi.enabled=true)

fail() { echo "FAIL: $*" >&2; exit 1; }
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

configmap="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# envFrom: the api Deployment mounts the chart ConfigMap.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# Chart vs binary: every env var cmd/api reads is rendered into the Deployment
# when every optional value is set. The names come from the binary's own
# literals: os.Getenv("X"), getenv("X", ...), env("X") and the const
# <name>Env = "X" the consumer groups are declared with.
binary_env() {
  local f
  {
    for f in "$repo_dir"/cmd/api/*.go; do
      [[ "$f" == *_test.go ]] && continue
      grep -hoE '(Getenv|getenv|env)\("[A-Z][A-Z0-9_]*"' "$f" || true
      grep -hoE '[A-Za-z]+Env += *"[A-Z][A-Z0-9_]*"' "$f" || true
    done
  } | sed -E 's/.*"([A-Z][A-Z0-9_]*)".*/\1/' | sort -u
}
# shellcheck disable=SC2207
api_vars=($(binary_env))
(( ${#api_vars[@]} >= 10 )) || fail "found only ${#api_vars[@]} env literals in cmd/api; the scan is looking in the wrong place"
rendered_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}"; do
  grep -q "name: $var\$" <<<"$rendered_all" || fail "the api Deployment does not render $var, which cmd/api reads"
done
# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
full_configmap="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --show-only templates/configmap.yaml)"
while read -r key; do
  [[ " ${api_vars[*]} " == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")
# Every variable the binary names as mandatory is rendered.
for var in $(grep -ho 'errors.New("[A-Z_]* is required' "$repo_dir"/cmd/api/config.go | sed -E 's/errors.New\("//; s/ is required//' | sort -u) \
  LINEHAUL_CONSUMER_GROUP NODE_TOPOLOGY_CONSUMER_GROUP SHIPPING_CONSUMER_GROUP; do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the api Deployment does not render $var, which cmd/api requires"
done

# Tuned config.* values reach the Deployment and the ConfigMap the api mounts.
for kv in OUTBOX_RELAY_INTERVAL=2s OUTBOX_MAX_ATTEMPTS=5 SHUTDOWN_DRAIN_DELAY=7s LOG_LEVEL=debug HTTP_ADDR=:9090; do
  [[ "$(env_value "$rendered_all" "${kv%%=*}")" == "${kv#*=}" ]] || fail "config value for ${kv%%=*} is not rendered into the Deployment"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$full_configmap" || fail "config value for ${kv%%=*} is not rendered into the ConfigMap"
done
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20"
grep -q 'OUTBOX_RELAY_INTERVAL' <<<"$configmap" && fail "OUTBOX_RELAY_INTERVAL must stay unrendered while empty (the binary default applies)"
unlimited="$(helm template "$release" "$chart_dir" "${base[@]}" --set config.outboxMaxAttempts=0 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "0"' <<<"$unlimited" || fail "config.outboxMaxAttempts=0 is not rendered"

# Mandatory configuration is rendered from values.
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
for g in "${groups[@]}"; do
  var="$(sed -E 's/([a-z])([A-Z])/\1_\2/g' <<<"${g%%:*}" | tr '[:lower:]' '[:upper:]')"
  [[ "$(env_value "$api_deployment" "$var")" == "${g#*:}" ]] || fail "the api Deployment does not render $var=${g#*:}"
  grep -q "^  $var: \"${g#*:}\"" <<<"$configmap" || fail "the ConfigMap does not carry $var=${g#*:}"
  [[ "${g#*:}" == last-mile-delivery-* ]] || fail "test fixture: group ${g#*:} is not service-prefixed"
done
# A group id is never derived from the pod name.
if grep -qE 'fieldRef|metadata\.name|POD_NAME|HOSTNAME' <<<"$api_deployment"; then
  fail "a consumer group (or anything else) must not be derived from the pod name"
fi

# DATABASE_URL: Secret only (secretKeyRef), never the ConfigMap.
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$full_configmap"; then fail "a DSN must never land in the ConfigMap"; fi
if grep -q 'example.invalid:5432' <<<"$full_configmap"; then fail "the DSN leaked into the ConfigMap"; fi
grep -A5 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' \
  || fail "MIGRATIONS_DATABASE_URL must be an optional secretKeyRef (the binary falls back to DATABASE_URL)"
existing="$(helm template "$release" "$chart_dir" --set database.existingSecret=my-db --set kafka.brokers=k:9092 \
  --set config.linehaulConsumerGroup=a --set config.nodeTopologyConsumerGroup=b --set config.shippingConsumerGroup=c)"
grep -A4 'name: DATABASE_URL$' <<<"$existing" | grep -q 'name: my-db' || fail "database.existingSecret is not used for DATABASE_URL"
if grep -q 'kind: Secret' <<<"$existing"; then fail "a Secret must not be created when database.existingSecret is set"; fi

# No *_MODE or permissive/in-memory wording anywhere in the render or NOTES.
render_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}")"
if grep -qE '^\s+- name: [A-Z_]*_MODE$' <<<"$render_all"; then fail "the render carries a *_MODE variable"; fi
if grep -qiE 'in-memory|silently runs|permissive' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt mentions an in-memory / permissive mode"
fi

# Pod hardening.
for needle in 'runAsNonRoot: true' 'readOnlyRootFilesystem: true' 'allowPrivilegeEscalation: false' '- ALL' 'type: RuntimeDefault' \
  'path: /healthz' 'path: /readyz'; do
  grep -q -- "$needle" <<<"$api_deployment" || fail "the api Deployment lacks: $needle"
done
grep -A3 'capabilities:' <<<"$api_deployment" | grep -q 'drop:' || fail "capabilities.drop is missing"
[[ "$(grep -c 'readOnlyRootFilesystem: true' <<<"$api_deployment")" == 1 ]] || fail "readOnlyRootFilesystem must be set on the container"
if grep -qE 'privileged: true|readOnlyRootFilesystem: false|allowPrivilegeEscalation: true' <<<"$api_deployment"; then fail "weakened hardening in the render"; fi

# Shutdown room: the floor is computed from the binary's own bounds
# (cmd/api/main.go): drain delay + HTTP shutdownTimeout + workerDrainTimeout for
# every consumer (awaited one after the other) + workerDrainTimeout for the relay.
const_seconds() { grep -E "^[[:space:]]+$1[[:space:]]*=" "$repo_dir/cmd/api/main.go" | sed -E 's/.*= *([0-9]+) *\* *time.Second.*/\1/'; }
http_s="$(const_seconds shutdownTimeout)"; worker_s="$(const_seconds workerDrainTimeout)"; drain_s="$(const_seconds defaultShutdownDrainDelay)"
[[ -n "$http_s" && -n "$worker_s" && -n "$drain_s" ]] || fail "could not read the shutdown bounds from cmd/api/main.go"
consumers="${#groups[@]}"
floor=$(( drain_s + http_s + worker_s * (consumers + 1) ))
[[ "$(env_value "$api_deployment" SHUTDOWN_DRAIN_DELAY)" == "${drain_s}s" ]] || fail "the chart default drain delay differs from the binary's (${drain_s}s)"
grace="$(grep -m1 'terminationGracePeriodSeconds:' <<<"$api_deployment" | sed -E 's/.*: *//')"
(( grace >= floor )) || fail "terminationGracePeriodSeconds $grace is below the sum of the binary's shutdown bounds ($floor)"

# --- refusals: the mandatory values name themselves ---------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
mandatory_except() { # mandatory_except <database|kafka|<group key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for g in "${groups[@]}"; do
  refusal_without "${g%%:*}" "config.${g%%:*}"
  expect_refusal "blank ${g%%:*}" "config.${g%%:*}" "${base[@]}" --set "config.${g%%:*}= "
done
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=LINEHAUL_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "a lowercase-valued *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=DEMAND_MODE' --set 'extraEnv[0].value=kafka'
helm template "$release" "$chart_dir" "${base[@]}" --set 'extraEnv[0].name=GOMAXPROCS' --set 'extraEnv[0].value=2' | grep -q 'name: GOMAXPROCS' \
  || fail "a legitimate extraEnv entry must render"
expect_refusal "a grace period below the floor" "terminationGracePeriodSeconds" "${base[@]}" --set "terminationGracePeriodSeconds=$((floor - 1))"
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
helm template "$release" "$chart_dir" "${base[@]}" --set "terminationGracePeriodSeconds=$floor" >/dev/null \
  || fail "a $floor s grace period must still render"
# The floor follows the drain delay.
expect_refusal "a long drain delay with the default grace period" "terminationGracePeriodSeconds" "${base[@]}" --set config.shutdownDrainDelay=30s
helm template "$release" "$chart_dir" "${base[@]}" --set config.shutdownDrainDelay=30s --set terminationGracePeriodSeconds=$((30 + floor - drain_s)) >/dev/null \
  || fail "a 30s drain delay with a grace period of its own floor must render"
expect_refusal "a drain delay that is not whole seconds" "config.shutdownDrainDelay" "${base[@]}" --set config.shutdownDrainDelay=1m

# Optional resources are off by default and render when asked.
default_render="$(helm template "$release" "$chart_dir" "${base[@]}")"
for kind in HorizontalPodAutoscaler Ingress HTTPRoute; do
  if grep -q "kind: $kind" <<<"$default_render"; then fail "$kind must not render by default"; fi
  grep -q "kind: $kind" <<<"$render_all" || fail "$kind did not render when enabled"
done
if grep -q 'replicas:' <<<"$rendered_all"; then fail "the Deployment must omit replicas while the HPA owns them"; fi

echo "PASS: last-mile-delivery chart render assertions"
