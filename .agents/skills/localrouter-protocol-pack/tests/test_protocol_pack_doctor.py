from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path

SCRIPT = Path(__file__).parents[1] / "scripts" / "protocol_pack_doctor.py"
SPEC = importlib.util.spec_from_file_location("protocol_pack_doctor", SCRIPT)
assert SPEC and SPEC.loader
doctor_module = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = doctor_module
SPEC.loader.exec_module(doctor_module)


class FixtureDoctor(doctor_module.Doctor):
    def __init__(self) -> None:
        super().__init__("http://127.0.0.1:8317", 1)
        pack = {
            "id": "sample",
            "mount": "/p/sample",
            "routes": [
                {
                    "operation_key": "sample.items.list",
                    "operation_id": "items.list",
                    "operation_id_role": "semantic-selector",
                    "operation_id_is_url": False,
                    "capabilities": ["dataset.read"],
                    "methods": ["GET"],
                    "path": "/items",
                    "call_url": "/p/sample/items",
                    "call": {
                        "methods": ["GET"],
                        "default_method": "GET",
                        "url": "/p/sample/items",
                        "authenticated": True,
                    },
                }
            ],
            "guides": [
                {
                    "id": "quickstart",
                    "operations": ["items.list"],
                    "markdown_url": "/docs/packs/sample/guides/quickstart.md",
                }
            ],
            "docs": {
                "manifest": "/docs/packs/sample/manifest.json",
                "markdown": "/docs/packs/sample/guide.md",
                "examples": "/docs/packs/sample/examples.json",
                "html": "/docs/packs/sample",
            },
        }
        self.responses = {
            "/.well-known/localrouter.json": {
                "schema_version": "1",
                "name": "LocalRouter",
                "scope": "loopback",
                "contract": {"digest": "a" * 64, "schema_version": "5"},
                "agent": {
                    "catalog": "/agent/operations",
                    "resolve": "/agent/resolve",
                    "compare": "/agent/compare",
                    "describe": "/agent/operations/{pack}/{operation}",
                    "preflight": "/agent/preflight",
                    "whoami": "/agent/whoami",
                    "docs": "/docs/agent.json",
                    "selection_mode": "agent",
                    "merged": False,
                },
                "maintenance": {
                    "mcp": "/manage/mcp",
                    "auth": {
                        "default": "administrator",
                        "administrator": {
                            "type": "header",
                            "header": "X-Local-Admin",
                        },
                        "agent_token": {
                            "enabled": False,
                            "type": "bearer",
                            "header": "Authorization",
                            "required_capability": "localrouter.maintain",
                            "service_access": False,
                        },
                    },
                    "operator_api": {
                        "auth": {"type": "header", "header": "X-Local-Admin"}
                    },
                },
                "authentication": {"type": "bearer", "header": "Authorization", "applies_to": ["/agent/*"]},
                "documentation": {
                    "openapi": "/docs/openapi.json",
                    "index": "/docs/index.json",
                },
                "protocols": [pack],
            },
            "/docs/openapi.json": {
                "paths": {
                    "/p/sample/items": {
                        "get": {
                            "x-localrouter-operation-id": "items.list",
                            "x-localrouter-operation-id-role": "semantic-selector",
                            "x-localrouter-operation-id-is-url": False,
                            "x-localrouter-call-url": "/p/sample/items",
                        }
                    }
                }
            },
            "/docs/index.json": {"contract_digest": "a" * 64, "data": [{"id": "sample"}]},
            "/docs/agent.json": {
                "schema_version": "5",
                "contract_digest": "a" * 64,
                "selection": {"mode": "agent", "merged": False},
                "endpoints": {
                    "catalog": {"path": "/agent/operations"},
                    "compare": {"path": "/agent/compare"},
                    "preflight": {"upstream_called": False},
                },
            },
            "/docs/packs/sample/manifest.json": pack,
            "/docs/packs/sample/examples.json": {
                "data": [
                    {
                        "operation_id": "items.list",
                        "call_url": "/p/sample/items",
                    }
                ]
            },
            "/docs/packs/sample/guide.md": b"guide",
            "/docs/packs/sample": b"html",
            "/docs/packs/sample/guides/quickstart.md": b"quickstart",
        }

    def fetch(self, path: str, expect_json: bool = True):
        return self.responses[path]


class DoctorTests(unittest.TestCase):
    def test_loopback_only(self) -> None:
        self.assertEqual(
            doctor_module.validate_base_url("http://127.0.0.1:8317"),
            "http://127.0.0.1:8317",
        )
        self.assertEqual(
            doctor_module.validate_base_url("http://localhost:8317/"),
            "http://localhost:8317",
        )
        with self.assertRaises(ValueError):
            doctor_module.validate_base_url("https://example.com")

    def test_consistent_public_contract_passes_without_overclaiming(self) -> None:
        checks = FixtureDoctor().run("sample")
        self.assertFalse(any(item.status == "fail" for item in checks))
        self.assertTrue(
            any(item.layer == "Syntax" and item.status == "pass" for item in checks)
        )
        self.assertTrue(
            any(
                item.layer == "Security" and item.status == "not-covered"
                for item in checks
            )
        )
        self.assertTrue(
            any(
                item.layer == "REST" and item.status == "not-covered" for item in checks
            )
        )

    def test_unknown_pack_fails(self) -> None:
        checks = FixtureDoctor().run("missing")
        self.assertTrue(
            any(item.status == "fail" and item.pack == "missing" for item in checks)
        )

    def test_openapi_drift_fails(self) -> None:
        doctor = FixtureDoctor()
        doctor.responses["/docs/openapi.json"] = {"paths": {}}
        checks = doctor.run("sample")
        self.assertTrue(
            any(item.layer == "OpenAPI" and item.status == "fail" for item in checks)
        )

    def test_operation_id_cannot_replace_call_url(self) -> None:
        doctor = FixtureDoctor()
        route = doctor.responses["/.well-known/localrouter.json"]["protocols"][0]["routes"][0]
        route["call_url"] = "/p/sample/items.list"
        checks = doctor.run("sample")
        self.assertTrue(
            any(item.layer == "Contract" and item.status == "fail" for item in checks)
        )


if __name__ == "__main__":
    unittest.main()
