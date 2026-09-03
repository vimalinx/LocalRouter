#!/usr/bin/env python3
"""Read-only LocalRouter discovery and documentation consistency doctor."""

from __future__ import annotations

import argparse
import ipaddress
import json
import re
import sys
from dataclasses import asdict, dataclass
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urljoin, urlparse
from urllib.request import HTTPRedirectHandler, Request, build_opener

MAX_RESPONSE_BYTES = 8 << 20
PACK_ID = re.compile(r"^[a-z][a-z0-9-]{1,31}$")
CAPABILITY_ID = re.compile(r"^[a-z][a-z0-9._-]{1,63}$")
DIGEST = re.compile(r"^[0-9a-f]{64}$")


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # type: ignore[no-untyped-def]
        raise HTTPError(req.full_url, code, "redirects are not accepted", headers, fp)


@dataclass
class Check:
    layer: str
    status: str
    evidence: str
    pack: str | None = None


class Doctor:
    def __init__(self, base_url: str, timeout: float) -> None:
        self.base_url = validate_base_url(base_url)
        self.timeout = timeout
        self.opener = build_opener(NoRedirect)
        self.checks: list[Check] = []

    def add(
        self, layer: str, status: str, evidence: str, pack: str | None = None
    ) -> None:
        self.checks.append(Check(layer, status, evidence, pack))

    def fetch(self, path: str, expect_json: bool = True) -> Any:
        url = urljoin(self.base_url + "/", path.lstrip("/"))
        parsed = urlparse(url)
        if (
            parsed.scheme not in {"http", "https"}
            or parsed.netloc != urlparse(self.base_url).netloc
        ):
            raise ValueError(f"discovery link leaves LocalRouter origin: {path}")
        request = Request(
            url, headers={"Accept": "application/json, text/markdown, text/html"}
        )
        with self.opener.open(request, timeout=self.timeout) as response:
            content_length = response.headers.get("Content-Length")
            if content_length and int(content_length) > MAX_RESPONSE_BYTES:
                raise ValueError(
                    f"response is larger than {MAX_RESPONSE_BYTES} bytes: {path}"
                )
            data = response.read(MAX_RESPONSE_BYTES + 1)
            if len(data) > MAX_RESPONSE_BYTES:
                raise ValueError(
                    f"response is larger than {MAX_RESPONSE_BYTES} bytes: {path}"
                )
        if not expect_json:
            return data
        return json.loads(data)

    def run(self, selected_pack: str | None) -> list[Check]:
        try:
            discovery = self.fetch("/.well-known/localrouter.json")
        except (HTTPError, URLError, ValueError, json.JSONDecodeError) as error:
            self.add("Discovery", "fail", f"cannot read discovery: {error}")
            return self.checks

        protocols = discovery.get("protocols") if isinstance(discovery, dict) else None
        compatibility_packs = (
            discovery.get("compatibility_packs")
            if isinstance(discovery, dict)
            else None
        )
        valid_discovery = (
            discovery.get("schema_version") == "1"
            and discovery.get("name") == "LocalRouter"
            and discovery.get("scope") == "loopback"
            and isinstance(protocols, list)
            and isinstance(compatibility_packs, list)
        )
        self.add(
            "Discovery",
            "pass" if valid_discovery else "fail",
            f"schema={discovery.get('schema_version')!r}, scope={discovery.get('scope')!r}, compatibility_packs={len(compatibility_packs or [])}, protocol_packs={len(protocols or [])}",
        )
        if not valid_discovery:
            return self.checks

        surfaces = discovery.get("surfaces") or []
        pack_model = discovery.get("pack_model") or {}
        topology = discovery.get("topology") or {}
        surface_ids = {
            surface.get("id")
            for surface in surfaces
            if isinstance(surface, dict)
        }
        topology_ok = (
            pack_model.get("unit") == "service-pack"
            and surface_ids == {"compatibility", "protocol", "workflow", "mcp"}
            and isinstance(topology.get("digest"), str)
            and bool(DIGEST.fullmatch(topology.get("digest")))
            and topology.get("schema_version")
            == (discovery.get("contract") or {}).get("schema_version")
        )
        self.add(
            "Service topology",
            "pass" if topology_ok else "fail",
            f"digest={topology.get('digest')!r}; all services use compatibility or Protocol Pack views; workflows and MCP are projections",
        )

        maintenance = discovery.get("maintenance") or {}
        authentication = discovery.get("authentication") or {}
        maintenance_auth = maintenance.get("auth") or {}
        administrator_auth = maintenance_auth.get("administrator") or {}
        agent_auth = maintenance_auth.get("agent_token") or {}
        auth_ok = (
            maintenance.get("mcp") == "/manage/mcp"
            and maintenance_auth.get("default") == "administrator"
            and administrator_auth.get("type") == "header"
            and administrator_auth.get("header") == "X-Local-Admin"
            and isinstance(agent_auth.get("enabled"), bool)
            and agent_auth.get("type") == "bearer"
            and agent_auth.get("header") == "Authorization"
            and agent_auth.get("required_capability") == "localrouter.maintain"
            and agent_auth.get("service_access") is False
            and maintenance.get("operator_api", {}).get("auth", {}).get("header")
            == "X-Local-Admin"
            and authentication.get("type") == "bearer"
            and authentication.get("header") == "Authorization"
            and "/agent/*" in (authentication.get("applies_to") or [])
        )
        self.add(
            "Authorization surface",
            "pass" if auth_ok else "fail",
            f"service Tokens are call-only; administrator maintenance is primary; optional Agent maintenance enabled={agent_auth.get('enabled')!r}",
        )

        contract = discovery.get("contract") or {}
        agent = discovery.get("agent") or {}
        contract_digest = contract.get("digest")
        agent_contract_ok = (
            isinstance(contract_digest, str)
            and bool(DIGEST.fullmatch(contract_digest))
            and isinstance(contract.get("schema_version"), str)
            and bool(contract.get("schema_version"))
            and agent.get("catalog") == "/agent/operations"
            and agent.get("resolve") == "/agent/resolve"
            and agent.get("compare") == "/agent/compare"
            and agent.get("preflight") == "/agent/preflight"
            and agent.get("whoami") == "/agent/whoami"
            and agent.get("docs") == "/docs/agent.json"
            and agent.get("selection_mode") == "agent"
            and agent.get("merged") is False
        )
        try:
            agent_docs = self.fetch(agent.get("docs", "/docs/agent.json"))
            agent_contract_ok = agent_contract_ok and (
                agent_docs.get("contract_digest") == contract_digest
                and agent_docs.get("schema_version") == contract.get("schema_version")
                and agent_docs.get("selection", {}).get("mode") == "agent"
                and agent_docs.get("selection", {}).get("merged") is False
                and agent_docs.get("endpoints", {}).get("catalog", {}).get("path")
                == "/agent/operations"
                and agent_docs.get("endpoints", {}).get("compare", {}).get("path")
                == "/agent/compare"
                and agent_docs.get("endpoints", {}).get("preflight", {}).get("upstream_called")
                is False
            )
        except (HTTPError, URLError, ValueError, json.JSONDecodeError) as error:
            agent_contract_ok = False
            agent_docs = {"error": str(error)}
        self.add(
            "Agent decision",
            "pass" if agent_contract_ok else "fail",
            f"digest={contract_digest!r}; independent catalog/resolve/compare/describe/preflight/whoami and public Agent contract agree",
        )

        try:
            openapi = self.fetch(
                (discovery.get("documentation") or {}).get(
                    "openapi", "/docs/openapi.json"
                )
            )
            index = self.fetch(
                (discovery.get("documentation") or {}).get("index", "/docs/index.json")
            )
        except (HTTPError, URLError, ValueError, json.JSONDecodeError) as error:
            self.add(
                "Documentation", "fail", f"cannot read OpenAPI or docs index: {error}"
            )
            return self.checks

        index_data = index.get("data") if isinstance(index, dict) else None
        if index.get("contract_digest") != contract_digest:
            self.add(
                "Agent decision",
                "fail",
                "docs index contract_digest differs from discovery",
            )
        index_ids = {
            item.get("id") for item in index_data or [] if isinstance(item, dict)
        }
        openapi_paths = openapi.get("paths") or {} if isinstance(openapi, dict) else {}

        by_id = {pack.get("id"): pack for pack in protocols if isinstance(pack, dict)}
        compatibility_by_id = {
            pack.get("id"): pack
            for pack in compatibility_packs
            if isinstance(pack, dict)
        }
        if selected_pack:
            if selected_pack in by_id and selected_pack in compatibility_by_id:
                self.add(
                    "Discovery",
                    "fail",
                    f"requested Pack {selected_pack!r} is ambiguous across Pack kinds",
                    selected_pack,
                )
                return self.checks
            if selected_pack not in by_id and selected_pack not in compatibility_by_id:
                self.add(
                    "Discovery",
                    "fail",
                    f"requested Pack {selected_pack!r} is not published",
                    selected_pack,
                )
                return self.checks
            by_id = (
                {selected_pack: by_id[selected_pack]}
                if selected_pack in by_id
                else {}
            )
            compatibility_by_id = (
                {selected_pack: compatibility_by_id[selected_pack]}
                if selected_pack in compatibility_by_id
                else {}
            )

        for pack_id, pack in sorted(compatibility_by_id.items()):
            self.check_compatibility_pack(pack_id, pack)

        for pack_id, pack in sorted(by_id.items()):
            if not isinstance(pack_id, str) or not PACK_ID.fullmatch(pack_id):
                self.add("Contract", "fail", f"invalid Pack id {pack_id!r}")
                continue
            self.check_pack(pack_id, pack, index_ids, openapi_paths)

        self.add("REST", "not-covered", "doctor does not invoke provider operations")
        self.add(
            "Streaming",
            "not-covered",
            "terminal SSE/chunked exchange requires a retained invocation",
        )
        self.add(
            "WebSocket/gRPC",
            "not-covered",
            "terminal frame/message exchange requires a retained invocation",
        )
        self.add(
            "Workflow",
            "not-covered",
            "provider terminal/cancel/restart behavior requires a retained job",
        )
        self.add(
            "Cost/quota",
            "not-covered",
            "public doctor does not prove provider balance, quote, or paid task success",
        )
        self.add(
            "Security",
            "not-covered",
            "private file modes, target ownership, and secret redaction require repository or authenticated evidence",
        )
        self.add(
            "Release",
            "not-covered",
            "reviewed plan and exact digest apply require authorized maintainer evidence",
        )
        self.add(
            "Recovery",
            "not-covered",
            "rollback requires a known revision and post-rollback verification",
        )
        return self.checks

    def check_compatibility_pack(self, pack_id: str, pack: dict[str, Any]) -> None:
        routes = pack.get("routes") or []
        mounts = pack.get("mounts") or []
        pool = pack.get("pool") or {}
        forbidden = {"base_url", "auth", "channels", "models", "credentials"}
        contract_ok = (
            isinstance(pack_id, str)
            and bool(PACK_ID.fullmatch(pack_id))
            and pack.get("pack_key") == f"compatibility:{pack_id}"
            and pack.get("kind") == "compatibility"
            and isinstance(pack.get("ready"), bool)
            and pack.get("status")
            in {"ready", "channel-not-ready", "state-unavailable"}
            and bool(mounts)
            and all(
                isinstance(mount, str)
                and (
                    mount == "/v1"
                    or mount.startswith("/v1/")
                    or mount == "/v1beta"
                )
                for mount in mounts
            )
            and pool.get("mode") == "channels"
            and isinstance(pool.get("eligible"), int)
            and pool.get("eligible") >= 0
            and bool(routes)
            and not forbidden.intersection(pack)
        )
        for route in routes:
            contract_ok = contract_ok and (
                isinstance(route, dict)
                and bool(route.get("methods"))
                and route.get("path") == route.get("call_url")
                and route.get("transport") == "http"
                and route.get("status") in {"available", "unsupported"}
            )
        self.add(
            "Compatibility Pack",
            "pass" if contract_ok else "fail",
            f"{len(routes)} standard routes, channel pool eligible={pool.get('eligible')!r}; private upstream and channel records omitted",
            pack_id,
        )

    def check_pack(
        self,
        pack_id: str,
        pack: dict[str, Any],
        index_ids: set[Any],
        openapi_paths: dict[str, Any],
    ) -> None:
        routes = pack.get("routes") or []
        guides = pack.get("guides") or []
        operation_ids = [
            route.get("operation_id") for route in routes if isinstance(route, dict)
        ]
        operation_set = {item for item in operation_ids if isinstance(item, str)}
        contract_ok = (
            pack.get("pack_key") == f"protocol:{pack_id}"
            and pack.get("kind") == "protocol"
            and len(operation_ids) == len(operation_set)
            and all(isinstance(item, str) and item for item in operation_ids)
            and bool(routes)
        )
        capability_count = 0
        # A route is executable discovery, not a hint that forces consumers to
        # invent a URL. Validate the runtime-derived binding independently of
        # operation_id syntax (notably dotted selectors such as chat.completions).
        invocation_failures: list[str] = []
        mount = pack.get("mount")
        for route in routes:
            if not isinstance(route, dict):
                contract_ok = False
                continue
            capabilities = route.get("capabilities") or []
            capability_count += len(capabilities)
            if len(capabilities) != len(set(capabilities)) or not all(
                isinstance(item, str) and CAPABILITY_ID.fullmatch(item)
                for item in capabilities
            ):
                contract_ok = False
            operation_id = route.get("operation_id")
            path = route.get("path")
            methods = route.get("methods") or []
            expected_url = (
                mount + path
                if isinstance(mount, str) and isinstance(path, str)
                else None
            )
            call = route.get("call") or {}
            binding_ok = (
                isinstance(operation_id, str)
                and route.get("operation_key") == f"{pack_id}.{operation_id}"
                and route.get("operation_id_role") == "semantic-selector"
                and route.get("operation_id_is_url") is False
                and isinstance(expected_url, str)
                and route.get("call_url") == expected_url
                and call.get("url") == expected_url
                and call.get("methods") == methods
                and bool(methods)
                and call.get("default_method") == methods[0]
                and call.get("authenticated") is True
            )
            if not binding_ok:
                contract_ok = False
                invocation_failures.append(str(operation_id))
            request_example = route.get("request_example")
            if request_example is not None and route.get("request_example_role") != "illustrative-shape":
                contract_ok = False
                invocation_failures.append(f"{operation_id}:request-example-role")
            if isinstance(request_example, dict) and "model" in request_example:
                model_input = (route.get("dynamic_inputs") or {}).get("model") or {}
                if not isinstance(model_input.get("rule"), str) or not model_input.get("rule"):
                    contract_ok = False
                    invocation_failures.append(f"{operation_id}:dynamic-model")
                source_key = model_input.get("source_operation_key")
                if isinstance(source_key, str):
                    source_operation = source_key.removeprefix(f"{pack_id}.")
                    source_route = next(
                        (
                            candidate
                            for candidate in routes
                            if isinstance(candidate, dict)
                            and candidate.get("operation_id") == source_operation
                        ),
                        None,
                    )
                    if (
                        not isinstance(source_route, dict)
                        or model_input.get("source_call_url")
                        != source_route.get("call_url")
                        or model_input.get("extract") != "data[].id"
                    ):
                        contract_ok = False
                        invocation_failures.append(f"{operation_id}:dynamic-model-source")
        for guide in guides:
            if not isinstance(guide, dict):
                contract_ok = False
                continue
            if not set(guide.get("operations") or []).issubset(operation_set):
                contract_ok = False
        self.add(
            "Syntax",
            "pass",
            "published Pack was accepted by the strict runtime loader",
            pack_id,
        )
        self.add(
            "Contract",
            "pass" if contract_ok else "fail",
            f"{len(operation_set)} unique operations, {capability_count} capability aliases; executable call_url bindings agree"
            if not invocation_failures
            else f"invalid invocation bindings: {', '.join(invocation_failures)}",
            pack_id,
        )

        docs = pack.get("docs") or {}
        document_links = {
            "manifest": docs.get("manifest"),
            "markdown": docs.get("markdown"),
            "examples": docs.get("examples"),
            "html": docs.get("html"),
        }
        document_ok = pack_id in index_ids
        failures: list[str] = []
        fetched_documents: dict[str, Any] = {}
        for name, path in document_links.items():
            if not isinstance(path, str) or not path.startswith("/"):
                document_ok = False
                failures.append(f"{name}:missing")
                continue
            try:
                fetched_documents[name] = self.fetch(
                    path, expect_json=name in {"manifest", "examples"}
                )
            except (HTTPError, URLError, ValueError, json.JSONDecodeError) as error:
                document_ok = False
                failures.append(f"{name}:{error}")
        manifest_routes = (fetched_documents.get("manifest") or {}).get("routes") or []
        example_routes = (fetched_documents.get("examples") or {}).get("data") or []
        published_bindings = {
            (route.get("operation_id"), route.get("call_url"))
            for route in routes
            if isinstance(route, dict)
        }
        manifest_bindings = {
            (route.get("operation_id"), route.get("call_url"))
            for route in manifest_routes
            if isinstance(route, dict)
        }
        example_bindings = {
            (route.get("operation_id"), route.get("call_url"))
            for route in example_routes
            if isinstance(route, dict)
        }
        if (
            published_bindings != manifest_bindings
            or published_bindings != example_bindings
        ):
            document_ok = False
            failures.append(
                "call_url bindings differ across discovery, Manifest, or examples"
            )
        for guide in guides:
            path = guide.get("markdown_url") if isinstance(guide, dict) else None
            if not isinstance(path, str):
                document_ok = False
                failures.append("guide:missing")
                continue
            try:
                self.fetch(path, expect_json=False)
            except (HTTPError, URLError, ValueError) as error:
                document_ok = False
                failures.append(f"guide:{error}")
        self.add(
            "Documentation",
            "pass" if document_ok else "fail",
            "index, Manifest, OpenAPI links, HTML, Markdown, examples, and guides agree"
            if document_ok
            else "; ".join(failures),
            pack_id,
        )

        missing_paths = []
        openapi_binding_failures = []
        for route in routes:
            if not isinstance(route, dict):
                continue
            path = route.get("path")
            full_path = (
                mount + path
                if isinstance(mount, str) and isinstance(path, str)
                else None
            )
            if (
                isinstance(mount, str)
                and isinstance(path, str)
                and full_path not in openapi_paths
            ):
                missing_paths.append(full_path)
                continue
            path_item = openapi_paths.get(full_path) or {}
            for method in route.get("methods") or []:
                operation = path_item.get(str(method).lower()) or {}
                if (
                    operation.get("x-localrouter-operation-id")
                    != route.get("operation_id")
                    or operation.get("x-localrouter-operation-id-role")
                    != "semantic-selector"
                    or operation.get("x-localrouter-operation-id-is-url") is not False
                    or operation.get("x-localrouter-call-url") != full_path
                ):
                    openapi_binding_failures.append(f"{method} {full_path}")
        self.add(
            "OpenAPI",
            "pass" if not missing_paths and not openapi_binding_failures else "fail",
            f"{len(routes)} routes represented"
            if not missing_paths and not openapi_binding_failures
            else "; ".join(
                filter(
                    None,
                    [
                        f"missing paths: {', '.join(missing_paths)}"
                        if missing_paths
                        else "",
                        f"invalid call bindings: {', '.join(openapi_binding_failures)}"
                        if openapi_binding_failures
                        else "",
                    ],
                )
            ),
            pack_id,
        )


def validate_base_url(raw: str) -> str:
    parsed = urlparse(raw.rstrip("/"))
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username
        or parsed.password
    ):
        raise ValueError("base URL must be a plain HTTP(S) LocalRouter origin")
    if parsed.hostname != "localhost":
        try:
            if not ipaddress.ip_address(parsed.hostname).is_loopback:
                raise ValueError("base URL must use a loopback host")
        except ValueError as error:
            if str(error) == "base URL must use a loopback host":
                raise
            raise ValueError("base URL must use localhost or a loopback IP") from error
    return raw.rstrip("/")


def render_markdown(checks: list[Check]) -> str:
    lines = ["| Layer | Pack | Status | Evidence |", "|---|---|---|---|"]
    for check in checks:
        evidence = check.evidence.replace("|", "\\|").replace("\n", " ")
        lines.append(
            f"| {check.layer} | {check.pack or 'all'} | {check.status} | {evidence} |"
        )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8317")
    parser.add_argument("--pack", help="limit checks to one published Pack id")
    parser.add_argument("--timeout", type=float, default=5.0)
    parser.add_argument("--format", choices=("json", "markdown"), default="json")
    args = parser.parse_args()
    if args.pack and not PACK_ID.fullmatch(args.pack):
        parser.error("--pack must match the LocalRouter Pack id format")
    if args.timeout <= 0 or args.timeout > 60:
        parser.error("--timeout must be between 0 and 60 seconds")
    try:
        doctor = Doctor(args.base_url, args.timeout)
    except ValueError as error:
        parser.error(str(error))
    checks = doctor.run(args.pack)
    failed = any(check.status == "fail" for check in checks)
    if args.format == "markdown":
        print(render_markdown(checks))
    else:
        print(
            json.dumps(
                {
                    "schema_version": "1",
                    "base_url": doctor.base_url,
                    "pack": args.pack,
                    "result": "fail" if failed else "pass",
                    "scope": "public discovery and documentation consistency only",
                    "checks": [asdict(check) for check in checks],
                },
                indent=2,
                ensure_ascii=False,
            )
        )
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
