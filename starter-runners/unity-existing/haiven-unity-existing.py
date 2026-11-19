#!/usr/bin/env python3
"""Integrate one immutable Haiven SDK revision into a caller-owned Unity project."""

from __future__ import annotations

import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from collections.abc import Mapping, Sequence
from pathlib import Path
from typing import Any, TextIO

PROTOCOL = "starter-runner/v1"
RECIPE_ID = "sdk/unity-existing"
RECIPE_VERSION = "1.0.2"
PACKAGE_NAME = "com.haiven.sdk"
PACKAGE_URL_PREFIX = "https://github.com/haiven-dev/haiven-sdk-unity.git?path=/com.haiven.sdk#"
SUPPORTED_HOSTS = {"linux", "darwin", "win32"}
MINIMUM_UNITY = (2022, 3)
REVISION = re.compile(r"^[0-9a-f]{40}$")
REQUEST_ID = re.compile(r"^[A-Za-z0-9._-]+$")
UNITY_VERSION = re.compile(r"^m_EditorVersion:\s*(\d+)\.(\d+)\.[^\s]+\s*$", re.MULTILINE)
RECIPE_MANIFEST = Path(__file__).with_name("recipe.json")

MANUAL_STEPS = {
    "plan": ["inspect-project"],
    "apply": ["pin-sdk-package", "import-sdk-package", "wire-sdk"],
    "verify": ["verify-package-lock", "verify-code-wiring"],
}


def digest_bytes(content: bytes) -> str:
    return "sha256:" + hashlib.sha256(content).hexdigest()


def digest_file(path: Path) -> str:
    return digest_bytes(path.read_bytes())


def package_url(revision: str) -> str:
    return PACKAGE_URL_PREFIX + revision


def result(
    request: Mapping[str, Any],
    status: str,
    *,
    changes: Sequence[Mapping[str, str]] = (),
    artifacts: Sequence[Mapping[str, str]] = (),
    resolved_sources: Mapping[str, str] | None = None,
    completed: Sequence[str] = (),
    pending: Sequence[str] | None = None,
    warnings: Sequence[str] = (),
    errors: Sequence[str] = (),
) -> dict[str, Any]:
    operation = str(request.get("operation", ""))
    return {
        "api_version": PROTOCOL,
        "request_id": str(request.get("request_id", "")),
        "operation": operation,
        "status": status,
        "changes": [dict(change) for change in changes],
        "artifacts": [dict(artifact) for artifact in artifacts],
        "resolved_sources": dict(resolved_sources or {}),
        "manual_steps": {
            "completed": list(completed),
            "pending": list(MANUAL_STEPS.get(operation, []) if pending is None else pending),
        },
        "warnings": list(warnings),
        "errors": list(errors),
    }


def malformed_request(request: Mapping[str, Any]) -> str | None:
    required = {
        "api_version",
        "request_id",
        "operation",
        "recipe",
        "destination",
        "parameters",
        "preview",
    }
    if set(request) != required:
        return "request fields do not match starter-runner/v1"
    if request.get("api_version") != PROTOCOL:
        return "unsupported request api_version"
    request_id = request.get("request_id")
    if not isinstance(request_id, str) or REQUEST_ID.fullmatch(request_id) is None:
        return "request_id has an invalid format"
    if request.get("operation") not in MANUAL_STEPS:
        return "operation must be plan, apply, or verify"
    recipe = request.get("recipe")
    if not isinstance(recipe, Mapping) or set(recipe) != {"id", "version", "digest"}:
        return "recipe must contain id, version, and digest"
    destination = request.get("destination")
    if not isinstance(destination, str) or not destination:
        return "destination must be a non-empty string"
    if not isinstance(request.get("parameters"), Mapping):
        return "parameters must be an object"
    if not isinstance(request.get("preview"), bool):
        return "preview must be a boolean"
    return None


def validate_identity(request: Mapping[str, Any]) -> str | None:
    recipe = request["recipe"]
    assert isinstance(recipe, Mapping)
    if recipe.get("id") != RECIPE_ID or recipe.get("version") != RECIPE_VERSION:
        return "request names a different recipe"
    digest = recipe.get("digest")
    if not isinstance(digest, str) or re.fullmatch(r"sha256:[0-9a-f]{64}", digest) is None:
        return "recipe digest must be sha256:<64 lowercase hex>"
    if digest != digest_file(RECIPE_MANIFEST):
        return "recipe digest does not match this runner"
    return None


def request_inputs(request: Mapping[str, Any]) -> tuple[Path, str, str] | str:
    parameters = request["parameters"]
    assert isinstance(parameters, Mapping)
    if set(parameters) - {"sdk_revision", "host_platform"}:
        return "undeclared parameters are not accepted; this runner accepts no secrets"
    revision = parameters.get("sdk_revision")
    if not isinstance(revision, str) or REVISION.fullmatch(revision) is None:
        return (
            "sdk_revision must be one full lowercase 40-hex Git commit from the Unity SDK repository"
        )
    host = parameters.get("host_platform", sys.platform)
    if not isinstance(host, str) or host not in SUPPORTED_HOSTS:
        return "host_platform must be linux, darwin, or win32"
    destination = Path(str(request["destination"]))
    if not destination.is_absolute():
        return "destination must be an absolute caller-owned Unity project path"
    return destination, revision, host


def inspect_project(project: Path) -> str | None:
    if not project.is_dir():
        return "destination is not an existing directory"
    required = (
        project / "Assets",
        project / "Packages" / "manifest.json",
        project / "ProjectSettings" / "ProjectVersion.txt",
    )
    if not required[0].is_dir():
        return "destination is not a Unity project: Assets is missing"
    for path in required[1:]:
        if not path.is_file():
            return f"destination is not a Unity project: {path.relative_to(project).as_posix()} is missing"
    try:
        version_text = required[2].read_text(encoding="utf-8")
    except (OSError, UnicodeError) as error:
        return f"cannot read ProjectSettings/ProjectVersion.txt: {error}"
    match = UNITY_VERSION.search(version_text)
    if match is None:
        return "ProjectSettings/ProjectVersion.txt has no supported m_EditorVersion"
    version = (int(match.group(1)), int(match.group(2)))
    if version < MINIMUM_UNITY:
        return "the Unity project is below the supported 2022.3 editor floor"
    try:
        manifest = json.loads(required[1].read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        return f"Packages/manifest.json is not readable JSON: {error}"
    if not isinstance(manifest, dict) or not isinstance(manifest.get("dependencies"), dict):
        return "Packages/manifest.json must contain a dependencies object"
    return None


def manifest_dependency(project: Path) -> str | None:
    document = json.loads((project / "Packages" / "manifest.json").read_text(encoding="utf-8"))
    value = document["dependencies"].get(PACKAGE_NAME)
    return value if isinstance(value, str) else None


def _skip_string(text: str, start: int) -> int:
    cursor = start + 1
    while cursor < len(text):
        if text[cursor] == "\\":
            cursor += 2
        elif text[cursor] == '"':
            return cursor + 1
        else:
            cursor += 1
    raise ValueError("unterminated JSON string")


def _skip_space(text: str, start: int) -> int:
    cursor = start
    while cursor < len(text) and text[cursor].isspace():
        cursor += 1
    return cursor


def _object_bounds_for_key(text: str, key: str) -> tuple[int, int]:
    cursor = _skip_space(text, 0)
    if cursor >= len(text) or text[cursor] != "{":
        raise ValueError("manifest root is not an object")
    cursor += 1
    while True:
        cursor = _skip_space(text, cursor)
        if cursor >= len(text) or text[cursor] == "}":
            break
        if text[cursor] != '"':
            raise ValueError("manifest object key is not a string")
        end = _skip_string(text, cursor)
        name = json.loads(text[cursor:end])
        cursor = _skip_space(text, end)
        if cursor >= len(text) or text[cursor] != ":":
            raise ValueError("manifest object key has no value")
        cursor = _skip_space(text, cursor + 1)
        if name == key:
            if cursor >= len(text) or text[cursor] != "{":
                raise ValueError(f"manifest {key} is not an object")
            depth = 1
            value_cursor = cursor + 1
            while value_cursor < len(text) and depth:
                if text[value_cursor] == '"':
                    value_cursor = _skip_string(text, value_cursor)
                    continue
                if text[value_cursor] == "{":
                    depth += 1
                elif text[value_cursor] == "}":
                    depth -= 1
                value_cursor += 1
            if depth:
                raise ValueError(f"manifest {key} object is not closed")
            return cursor, value_cursor
        decoder = json.JSONDecoder()
        _, consumed = decoder.raw_decode(text[cursor:])
        cursor += consumed
        cursor = _skip_space(text, cursor)
        if cursor < len(text) and text[cursor] == ",":
            cursor += 1
            continue
        if cursor < len(text) and text[cursor] == "}":
            break
        raise ValueError("manifest root object is malformed")
    raise ValueError(f"manifest has no {key} object")


def pin_manifest_text(text: str, desired: str) -> tuple[str, bool]:
    start, end = _object_bounds_for_key(text, "dependencies")
    body = text[start:end]
    dependency = re.compile(r'("com\.haiven\.sdk"\s*:\s*)"(?:\\.|[^"\\])*"')
    match = dependency.search(body)
    encoded = json.dumps(desired)
    if match is not None:
        updated_body = body[: match.start()] + match.group(1) + encoded + body[match.end() :]
        updated = text[:start] + updated_body + text[end:]
        return updated, updated != text
    closing = end - 1
    line_start = text.rfind("\n", 0, closing) + 1
    nonspace = _skip_space(text, start + 1)
    closing_line_prefix = text[line_start:closing]
    if closing_line_prefix.strip():
        separator = "" if nonspace == closing else ", "
        insertion = f"{json.dumps(PACKAGE_NAME)}: {encoded}{separator}"
    elif nonspace == closing:
        closing_indent = closing_line_prefix
        item_indent = closing_indent + "  "
        insertion = f"\n{item_indent}{json.dumps(PACKAGE_NAME)}: {encoded}\n{closing_indent}"
    else:
        closing_indent = closing_line_prefix
        item_indent = closing_indent + "  "
        insertion = f"\n{item_indent}{json.dumps(PACKAGE_NAME)}: {encoded},"
    updated = text[: start + 1] + insertion + text[start + 1 :]
    return updated, True


def overlapping_dirty_files(project: Path) -> list[str]:
    command = [
        "git",
        "-C",
        str(project),
        "status",
        "--porcelain=v1",
        "--untracked-files=all",
        "--",
        "Packages/manifest.json",
        "Packages/packages-lock.json",
    ]
    try:
        completed = subprocess.run(command, check=False, capture_output=True, text=True)
    except FileNotFoundError:
        if any((candidate / ".git").exists() for candidate in (project, *project.parents)):
            return ["Git is unavailable for the containing worktree"]
        return []
    if completed.returncode != 0:
        if any((candidate / ".git").exists() for candidate in (project, *project.parents)):
            return ["Git could not inspect the containing worktree"]
        return []
    return [line[3:] for line in completed.stdout.splitlines() if len(line) >= 4]


def atomic_write(path: Path, content: str) -> None:
    mode = path.stat().st_mode
    descriptor, temporary_name = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as output:
            output.write(content)
            output.flush()
            os.fsync(output.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if temporary.exists():
            temporary.unlink()


def lock_state(project: Path, desired: str) -> tuple[str, str | None]:
    lock_path = project / "Packages" / "packages-lock.json"
    if not lock_path.is_file():
        return (
            "pending",
            "Packages/packages-lock.json is absent; open the project in Unity and wait for import",
        )
    try:
        document = json.loads(lock_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        return "drift", f"Packages/packages-lock.json is not readable JSON: {error}"
    dependencies = document.get("dependencies") if isinstance(document, dict) else None
    entry = dependencies.get(PACKAGE_NAME) if isinstance(dependencies, dict) else None
    if not isinstance(entry, dict):
        return "pending", "Unity has not resolved com.haiven.sdk in Packages/packages-lock.json"
    if entry.get("source") != "git" or entry.get("version") != desired:
        return "drift", "Unity resolved com.haiven.sdk from a different source or revision"
    return "resolved", None


def handle(request: Mapping[str, Any]) -> dict[str, Any]:
    identity_error = validate_identity(request)
    if identity_error:
        return result(request, "failed", errors=[identity_error])
    inputs = request_inputs(request)
    if isinstance(inputs, str):
        return result(request, "failed", errors=[inputs])
    project, revision, _host = inputs
    project_error = inspect_project(project)
    if project_error:
        return result(request, "failed", errors=[project_error])

    operation = str(request["operation"])
    desired = package_url(revision)
    resolved = {"haiven-sdk-unity": revision}
    manifest_path = project / "Packages" / "manifest.json"
    current = manifest_dependency(project)
    manifest_action = "none" if current == desired else "replace"

    if operation == "plan":
        lock_path = project / "Packages" / "packages-lock.json"
        lock_action = "replace" if lock_path.exists() else "create"
        return result(
            request,
            "planned",
            changes=[
                {"path": "Packages/manifest.json", "action": manifest_action},
                {"path": "Packages/packages-lock.json", "action": lock_action},
            ],
            resolved_sources=resolved,
            completed=MANUAL_STEPS["plan"],
            pending=[],
        )

    if operation == "apply":
        if request.get("preview") is True:
            return result(request, "failed", errors=["preview=true is valid only for plan"])
        changed = False
        if current != desired:
            dirty = overlapping_dirty_files(project)
            if dirty:
                return result(
                    request,
                    "failed",
                    resolved_sources=resolved,
                    errors=[
                        "refusing to overwrite dirty starter-owned files; commit, stash, or restore Packages/manifest.json and Packages/packages-lock.json"
                    ],
                )
            text = manifest_path.read_text(encoding="utf-8")
            updated, changed = pin_manifest_text(text, desired)
            if changed:
                atomic_write(manifest_path, updated)
        return result(
            request,
            "manual-required",
            changes=[
                {
                    "path": "Packages/manifest.json",
                    "action": "replace" if changed else "none",
                },
                {"path": "Packages/packages-lock.json", "action": "none"},
            ],
            artifacts=[
                {
                    "path": "Packages/manifest.json",
                    "digest": digest_file(manifest_path),
                    "owner": RECIPE_ID,
                }
            ],
            resolved_sources=resolved,
            completed=["pin-sdk-package"],
            pending=["import-sdk-package", "wire-sdk"],
            warnings=[
                "Open this exact project in Unity, wait for package import, then wire the SDK through caller-owned game code."
            ],
        )

    state, detail = lock_state(project, desired)
    if current != desired:
        detail = "Packages/manifest.json does not pin the requested Haiven SDK revision"
        state = "drift"
    artifacts = [
        {
            "path": "Packages/manifest.json",
            "digest": digest_file(manifest_path),
            "owner": RECIPE_ID,
        }
    ]
    if state == "resolved":
        lock_path = project / "Packages" / "packages-lock.json"
        artifacts.append(
            {
                "path": "Packages/packages-lock.json",
                "digest": digest_file(lock_path),
                "owner": RECIPE_ID,
            }
        )
        return result(
            request,
            "manual-required",
            changes=[
                {"path": "Packages/manifest.json", "action": "none"},
                {"path": "Packages/packages-lock.json", "action": "none"},
            ],
            artifacts=artifacts,
            resolved_sources=resolved,
            completed=["verify-package-lock"],
            pending=["verify-code-wiring"],
            warnings=[
                "The immutable package is resolved; native inspection of caller-owned SDK wiring remains required."
            ],
        )
    return result(
        request,
        "manual-required",
        changes=[
            {"path": "Packages/manifest.json", "action": "none"},
            {"path": "Packages/packages-lock.json", "action": "none"},
        ],
        artifacts=artifacts,
        resolved_sources=resolved,
        completed=[],
        pending=MANUAL_STEPS["verify"],
        errors=[detail or "Unity package resolution is incomplete"],
    )


def main(
    argv: Sequence[str] | None = None,
    stdin: TextIO = sys.stdin,
    stdout: TextIO = sys.stdout,
    stderr: TextIO = sys.stderr,
) -> int:
    if argv:
        print("haiven-unity-existing reads one starter-runner/v1 request from stdin", file=stderr)
        return 2
    try:
        request = json.load(stdin)
        if not isinstance(request, Mapping):
            raise ValueError("request must be a JSON object")
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"invalid starter request: {error}", file=stderr)
        return 2
    problem = malformed_request(request)
    if problem:
        print(f"invalid starter request: {problem}", file=stderr)
        return 2
    try:
        response = handle(request)
    except (OSError, UnicodeError, ValueError, subprocess.SubprocessError) as error:
        response = result(request, "failed", errors=[str(error)])
    json.dump(response, stdout, sort_keys=True)
    stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
