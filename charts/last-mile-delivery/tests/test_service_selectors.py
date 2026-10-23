#!/usr/bin/env python3
"""Assert this chart's Service selector isolates the api component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates. A Service that selects on those two alone selects
ALL of them as soon as a second component exists. The Deployment here carries
`app.kubernetes.io/component` in selector.matchLabels and its pod labels, and the
Service selects on it, so each Service selects EXACTLY ONE Deployment. This test
fails if that ever stops being true.

last-mile-delivery has one component, `api`; the optional resources (HPA, Ingress,
HTTPRoute) are rendered ON together with it, and the api env is checked against the
variables cmd/api reads.

Run: python3 charts/last-mile-delivery/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
REPO_DIR = CHART_DIR.parents[1]
RELEASE = "last-mile-delivery"

# Dummy values, no password: the chart refuses to render without a database
# source, the Kafka brokers and the three consumer groups. None is ever dialled.
GROUPS = {
    "linehaulConsumerGroup": "last-mile-delivery-linehaul",
    "nodeTopologyConsumerGroup": "last-mile-delivery-node-topology",
    "shippingConsumerGroup": "last-mile-delivery-shipping",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a group key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in GROUPS.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "autoscaling.api.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    "--set", "config.outboxRelayInterval=2s",
]


def binary_env() -> set[str]:
    """Every env var cmd/api reads, from its own literals (tests excluded)."""
    names: set[str] = set()
    call = re.compile(r'(?:Getenv|getenv|env)\("([A-Z][A-Z0-9_]*)"')
    const = re.compile(r'\b[A-Za-z]+Env\s*=\s*"([A-Z][A-Z0-9_]*)"')
    for path in (REPO_DIR / "cmd" / "api").glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text()
        names.update(call.findall(text))
        names.update(const.findall(text))
    return names


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if len(deployments) != 1 or RELEASE not in deployments:
        failures.append(f"expected exactly the api Deployment {RELEASE!r}, got {sorted(deployments)}")
    if RELEASE not in services:
        failures.append("the api Service was not rendered")
    elif selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")

    # Routes target the api Service by name and port.
    api_port = services.get(RELEASE, {}).get("spec", {}).get("ports", [{}])[0].get("port")
    for d in docs:
        if d.get("kind") == "HTTPRoute":
            refs = [b for r in d["spec"]["rules"] for b in r["backendRefs"]]
            if any(b["name"] != RELEASE or b["port"] != api_port for b in refs):
                failures.append("the HTTPRoute must target the api Service and its port")
        if d.get("kind") == "Ingress":
            backends = [p["backend"]["service"] for r in d["spec"]["rules"] for p in r["http"]["paths"]]
            if any(b["name"] != RELEASE or b["port"]["number"] != api_port for b in backends):
                failures.append("the Ingress must target the api Service and its port")

    # The api HPA owns replicas when enabled.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    api = deployments.get(RELEASE)
    if api is None:
        return
    api_env = env_names(api)
    for want in sorted(binary_env()):
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want}, which cmd/api reads")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    modes = [n for n in api_env if n.endswith("_MODE")]
    if modes:
        failures.append(f"the api Deployment renders mode switches: {modes}")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=SHIPPING_MODE", "--set", "extraEnv[0].value=permissive"], "extraEnv sets SHIPPING_MODE"),
        ("a grace period under the shutdown bounds", BASE + ["--set", "terminationGracePeriodSeconds=20"], "terminationGracePeriodSeconds"),
        ("a drain delay that is not whole seconds", BASE + ["--set", "config.shutdownDrainDelay=1m"], "config.shutdownDrainDelay"),
    ]
    for key in GROUPS:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy an HPA or a route.
    defaults = render(BASE)
    stray = [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional resources rendered with default values: {stray}")
    dep = next(d for d in defaults if d.get("kind") == "Deployment")
    names = env_names(dep)
    for want in ("KAFKA_BROKERS", "DATABASE_URL", *(f"{k}" for k in ("LINEHAUL_CONSUMER_GROUP", "NODE_TOPOLOGY_CONSUMER_GROUP", "SHIPPING_CONSUMER_GROUP"))):
        if want not in names:
            failures.append(f"{want} is mandatory and must always be rendered")
    if "OUTBOX_RELAY_INTERVAL" in names:
        failures.append("OUTBOX_RELAY_INTERVAL must be off with default values (the binary default applies)")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: the api Service selects exactly one Deployment; HPA, Ingress and HTTPRoute are off by default and target the api; "
          "every cmd/api env var is rendered once; the chart refuses to render without a database source, brokers or any consumer "
          "group, with a *_MODE in extraEnv, or with a grace period under the shutdown bounds")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
