#!/usr/bin/env python3
"""Codex account inspection for CLIProxyAPI.

Runs once, intended to be scheduled by systemd timer or cron.
It never deletes accounts. It only auto-disables quota-exhausted accounts and
auto-enables disabled accounts whose weekly quota is available again.
"""

from __future__ import annotations

import base64
import json
import os
import sys
import time
import urllib.parse
import urllib.error
import urllib.request
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any


CODEX_USAGE_URL = "https://chatgpt.com/backend-api/wham/usage"
CODEX_REFRESH_URL = "https://auth.openai.com/oauth/token"
CODEX_CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann"
CODEX_REDIRECT_URI = "http://localhost:1455/auth/callback"
FIVE_HOUR_WINDOW_SECONDS = 18_000
WEEK_WINDOW_SECONDS = 604_800
DEFAULT_USER_AGENT = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"
DEFAULT_HISTORY_PATH = "/root/cliproxyapi/static/codex-inspection-history.json"
HISTORY_LIMIT = 10


@dataclass
class Settings:
    cpa_base_url: str
    management_key: str
    target_type: str
    threshold: float
    delay_seconds: float
    timeout_seconds: float
    user_agent: str
    dry_run: bool
    history_path: str
    enable_token_refresh: bool
    refresh_threshold_days: float
    refresh_disabled_only: bool


def env_bool(name: str, default: bool) -> bool:
    value = os.getenv(name)
    if value is None or value.strip() == "":
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


def env_float(name: str, default: float) -> float:
    value = os.getenv(name)
    if value is None or value.strip() == "":
        return default
    try:
        parsed = float(value)
    except ValueError:
        return default
    return parsed if parsed >= 0 else default


def read_settings() -> Settings:
    base_url = os.getenv("CPA_BASE_URL", "http://127.0.0.1:8317").strip().rstrip("/")
    management_key = os.getenv("CPA_MANAGEMENT_KEY", "").strip()
    if not management_key:
        raise SystemExit("CPA_MANAGEMENT_KEY is required")
    return Settings(
        cpa_base_url=base_url,
        management_key=management_key,
        target_type=os.getenv("CODEX_INSPECTION_TARGET_TYPE", "codex").strip().lower() or "codex",
        threshold=min(100.0, max(0.0, env_float("CODEX_INSPECTION_THRESHOLD", 100.0))),
        delay_seconds=env_float("CODEX_INSPECTION_DELAY_SECONDS", 0.0),
        timeout_seconds=max(1.0, env_float("CODEX_INSPECTION_TIMEOUT_SECONDS", 15.0)),
        user_agent=os.getenv("CODEX_INSPECTION_USER_AGENT", DEFAULT_USER_AGENT).strip() or DEFAULT_USER_AGENT,
        dry_run=env_bool("CODEX_INSPECTION_DRY_RUN", False),
        history_path=os.getenv("CODEX_INSPECTION_HISTORY_PATH", DEFAULT_HISTORY_PATH).strip(),
        enable_token_refresh=env_bool("CODEX_INSPECTION_ENABLE_TOKEN_REFRESH", False),
        refresh_threshold_days=env_float("CODEX_INSPECTION_REFRESH_THRESHOLD_DAYS", 3.0),
        refresh_disabled_only=env_bool("CODEX_INSPECTION_REFRESH_DISABLED_ONLY", True),
    )


def current_millis() -> int:
    return int(time.time() * 1000)


def load_history(path: str) -> list[dict[str, Any]]:
    if not path:
        return []
    try:
        with open(path, "r", encoding="utf-8") as handle:
            data = json.load(handle)
    except FileNotFoundError:
        return []
    except Exception as exc:
        print(f"WARN failed to read history {path}: {exc}", file=sys.stderr)
        return []
    entries = data.get("entries") if isinstance(data, dict) else data
    return entries if isinstance(entries, list) else []


def history_key(entry: dict[str, Any]) -> tuple[str, str, str, str]:
    return (
        normalize_string(entry.get("fileName")),
        normalize_string(entry.get("action")),
        normalize_string(entry.get("reason")),
        normalize_string(entry.get("source")),
    )


def save_history(path: str, entries: list[dict[str, Any]]) -> None:
    if not path:
        return
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    tmp_path = f"{path}.tmp"
    payload = {
        "updatedAt": current_millis(),
        "entries": entries[:HISTORY_LIMIT],
    }
    with open(tmp_path, "w", encoding="utf-8") as handle:
        json.dump(payload, handle, ensure_ascii=False, separators=(",", ":"))
    os.replace(tmp_path, path)


def append_history(settings: Settings, entry: dict[str, Any]) -> None:
    if not settings.history_path:
        return
    entries = load_history(settings.history_path)
    key = history_key(entry)
    next_entries = []
    replaced = False
    for existing in entries:
        if isinstance(existing, dict) and history_key(existing) == key:
            next_entries.append({**existing, **entry, "id": normalize_string(existing.get("id")) or entry["id"]})
            replaced = True
        else:
            next_entries.append(existing)
    if not replaced:
        next_entries.insert(0, entry)
    next_entries = [item for item in next_entries if isinstance(item, dict)]
    next_entries.sort(key=lambda item: int(item.get("timestamp") or 0), reverse=True)
    try:
        save_history(settings.history_path, next_entries)
    except Exception as exc:
        print(f"WARN failed to write history {settings.history_path}: {exc}", file=sys.stderr)


def record_history(
    settings: Settings,
    result: dict[str, Any],
    kind: str,
    success: bool | None = None,
    error: str = "",
) -> None:
    action = normalize_string(result.get("action"))
    file_name = normalize_string(result.get("file"))
    if action == "keep" or not file_name:
        return
    timestamp = current_millis()
    entry: dict[str, Any] = {
        "id": f"timer-{kind}-{file_name}-{action}-{timestamp}",
        "timestamp": timestamp,
        "fileName": file_name,
        "account": normalize_string(result.get("label")) or "unknown",
        "action": action,
        "reason": normalize_string(result.get("reason")),
        "kind": kind,
        "source": "timer",
    }
    if success is not None:
        entry["success"] = success
    if error:
        entry["error"] = error
    append_history(settings, entry)


def request_json(settings: Settings, method: str, path: str, payload: Any | None = None) -> Any:
    data = None
    headers = {"Authorization": f"Bearer {settings.management_key}"}
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(
        f"{settings.cpa_base_url}/v0/management{path}",
        data=data,
        headers=headers,
        method=method,
    )
    try:
        with urllib.request.urlopen(req, timeout=settings.timeout_seconds) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            return json.loads(body) if body.strip() else None
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"{method} {path} failed: HTTP {exc.code} {body}") from exc


def request_management_raw(settings: Settings, method: str, path: str, payload: Any | None = None) -> Any:
    return request_json(settings, method, path, payload)


def download_auth_file(settings: Settings, file_name: str) -> dict[str, Any] | None:
    encoded = urllib.parse.urlencode({"name": file_name})
    data = request_management_raw(settings, "GET", f"/auth-files/download?{encoded}")
    return data if isinstance(data, dict) else None


def upload_auth_file(settings: Settings, file_name: str, token_data: dict[str, Any]) -> None:
    if settings.dry_run:
        print(f"DRY-RUN upload refreshed token {file_name}")
        return
    data = json.dumps(token_data, ensure_ascii=False).encode("utf-8")
    headers = {
        "Authorization": f"Bearer {settings.management_key}",
        "Content-Type": "application/json",
    }
    encoded = urllib.parse.urlencode({"name": file_name})
    req = urllib.request.Request(
        f"{settings.cpa_base_url}/v0/management/auth-files?{encoded}",
        data=data,
        headers=headers,
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=settings.timeout_seconds) as resp:
            resp.read()
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"POST /auth-files failed: HTTP {exc.code} {body}") from exc


def refresh_codex_token(settings: Settings, refresh_token: str) -> dict[str, Any]:
    payload = {
        "redirect_uri": CODEX_REDIRECT_URI,
        "grant_type": "refresh_token",
        "client_id": CODEX_CLIENT_ID,
        "refresh_token": refresh_token,
    }
    data = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(
        CODEX_REFRESH_URL,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=settings.timeout_seconds) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            parsed = json.loads(body) if body.strip() else {}
            return parsed if isinstance(parsed, dict) else {}
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"token refresh failed: HTTP {exc.code} {body}") from exc


def maybe_refresh_disabled_token(settings: Settings, file_item: dict[str, Any], action_after_quota: str) -> bool:
    if not settings.enable_token_refresh:
        return False
    file_name = normalize_string(file_item.get("name"))
    if not file_name:
        return False
    disabled = normalize_bool(file_item.get("disabled") or file_item.get("unavailable")) or action_after_quota == "disable"
    if settings.refresh_disabled_only and not disabled:
        return False

    token_detail = download_auth_file(settings, file_name)
    if not token_detail:
        return False
    refresh_token = normalize_string(token_detail.get("refresh_token") or token_detail.get("refreshToken"))
    if not refresh_token:
        return False
    remaining = remaining_seconds_from_detail(token_detail)
    threshold = settings.refresh_threshold_days * 86400
    if remaining is not None and remaining > threshold:
        return False

    label = account_label(file_item)
    result = {
        "file": file_name,
        "label": label,
        "action": "refresh",
        "reason": f"disabled token expires in {format_seconds(remaining)}, refresh threshold {settings.refresh_threshold_days:g}d",
    }
    record_history(settings, result, "issue")
    try:
        refreshed = refresh_codex_token(settings, refresh_token)
        access_token = normalize_string(refreshed.get("access_token"))
        if not access_token:
            raise RuntimeError("refresh response missing access_token")
        expires_in = number(refreshed.get("expires_in")) or 864000
        now = datetime.now(timezone.utc)
        next_data = dict(token_detail)
        next_data.update(
            {
                "access_token": access_token,
                "refresh_token": normalize_string(refreshed.get("refresh_token")) or refresh_token,
                "id_token": refreshed.get("id_token") or token_detail.get("id_token"),
                "last_refresh": now.strftime("%Y-%m-%dT%H:%M:%SZ"),
                "expired": datetime.fromtimestamp(time.time() + expires_in, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
            }
        )
        upload_auth_file(settings, file_name, next_data)
        if disabled:
            set_disabled(settings, file_name, True)
        record_history(settings, result, "execution", True)
        print(f"{label} -> refresh: success, new expiry in {format_seconds(expires_in)}")
        return True
    except Exception as exc:
        record_history(settings, result, "execution", False, str(exc))
        print(f"ERROR refresh {label}: {exc}", file=sys.stderr)
        return False


def normalize_string(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value.strip()
    if isinstance(value, (int, float)):
        return str(value)
    return ""


def normalize_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        return value.strip().lower() in {"1", "true", "yes", "on", "disabled"}
    return False


def normalize_provider(file_item: dict[str, Any]) -> str:
    return normalize_string(file_item.get("provider") or file_item.get("type")).lower()


def auth_index(file_item: dict[str, Any]) -> str:
    return normalize_string(file_item.get("authIndex") or file_item.get("auth_index"))


def account_label(file_item: dict[str, Any]) -> str:
    for key in ("displayAccount", "display_account", "account", "email", "name"):
        value = normalize_string(file_item.get(key))
        if value:
            return value
    return "unknown"


def nested_record(value: Any) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def account_id_from_jwt(token: str) -> str:
    parts = token.split(".")
    if len(parts) < 2:
        return ""
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    try:
        decoded = base64.urlsafe_b64decode(payload.encode("ascii"))
        data = json.loads(decoded.decode("utf-8"))
    except Exception:
        return ""
    return normalize_string(
        data.get("chatgpt_account_id")
        or data.get("chatgptAccountId")
        or data.get("account_id")
        or data.get("accountId")
    )


def jwt_payload(token: str) -> dict[str, Any]:
    parts = token.split(".")
    if len(parts) < 2:
        return {}
    payload = parts[1] + "=" * (-len(parts[1]) % 4)
    try:
        decoded = base64.urlsafe_b64decode(payload.encode("ascii"))
        data = json.loads(decoded.decode("utf-8"))
        return data if isinstance(data, dict) else {}
    except Exception:
        return {}


def token_remaining_seconds(access_token: str) -> float | None:
    payload = jwt_payload(access_token)
    exp = number(payload.get("exp"))
    if exp is None:
        return None
    return exp - time.time()


def parse_expired_remaining(value: str) -> float | None:
    text = normalize_string(value)
    if not text:
        return None
    try:
        normalized = text[:-1] + "+00:00" if text.endswith("Z") else text
        parsed = datetime.fromisoformat(normalized)
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=timezone.utc)
        return parsed.timestamp() - time.time()
    except Exception:
        return None


def remaining_seconds_from_detail(token_detail: dict[str, Any]) -> float | None:
    remaining = parse_expired_remaining(normalize_string(token_detail.get("expired") or token_detail.get("expires_at") or token_detail.get("expiresAt")))
    if remaining is not None:
        return remaining
    access_token = normalize_string(token_detail.get("access_token") or token_detail.get("accessToken"))
    if access_token:
        return token_remaining_seconds(access_token)
    return None


def format_seconds(seconds: float | None) -> str:
    if seconds is None:
        return "unknown"
    if seconds < 0:
        return "expired"
    minutes = int(seconds // 60)
    days = minutes // 1440
    hours = (minutes % 1440) // 60
    mins = minutes % 60
    if days > 0:
        return f"{days}d {hours}h"
    if hours > 0:
        return f"{hours}h {mins}m"
    return f"{mins}m"


def resolve_account_id(file_item: dict[str, Any]) -> str:
    for source in (file_item, nested_record(file_item.get("metadata")), nested_record(file_item.get("attributes"))):
        for key in ("chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"):
            value = normalize_string(source.get(key))
            if value:
                return value
        token = normalize_string(source.get("id_token") or source.get("idToken"))
        if token:
            value = account_id_from_jwt(token)
            if value:
                return value
    return ""


def parse_body(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, str) and value.strip():
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, dict) else {}
        except json.JSONDecodeError:
            return {}
    return {}


def number(value: Any) -> float | None:
    if isinstance(value, (int, float)):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value.strip())
        except ValueError:
            return None
    return None


def windows(rate_limit: dict[str, Any]) -> list[dict[str, Any]]:
    result = []
    for key in ("primary_window", "primaryWindow", "secondary_window", "secondaryWindow"):
        value = rate_limit.get(key)
        if isinstance(value, dict):
            result.append(value)
    return result


def used_percent(window: dict[str, Any] | None) -> float | None:
    if not window:
        return None
    return number(window.get("used_percent") or window.get("usedPercent"))


def classified_windows(rate_limit: dict[str, Any]) -> tuple[dict[str, Any] | None, dict[str, Any] | None]:
    five_hour = None
    weekly = None
    for window in windows(rate_limit):
        seconds = number(window.get("limit_window_seconds") or window.get("limitWindowSeconds"))
        if seconds == FIVE_HOUR_WINDOW_SECONDS:
            five_hour = window
        if seconds == WEEK_WINDOW_SECONDS:
            weekly = window
    return five_hour, weekly


def inspect_account(settings: Settings, file_item: dict[str, Any]) -> dict[str, Any]:
    index = auth_index(file_item)
    label = account_label(file_item)
    disabled = normalize_bool(file_item.get("disabled") or file_item.get("unavailable"))
    if not index:
        return {"file": file_item.get("name"), "label": label, "action": "keep", "reason": "missing auth_index"}

    headers = {
        "Authorization": "Bearer $TOKEN$",
        "Content-Type": "application/json",
        "User-Agent": settings.user_agent,
    }
    account_id = resolve_account_id(file_item)
    if account_id:
        headers["Chatgpt-Account-Id"] = account_id
    result = request_json(
        settings,
        "POST",
        "/api-call",
        {"authIndex": index, "method": "GET", "url": CODEX_USAGE_URL, "header": headers},
    )
    status_code = int(result.get("status_code") or result.get("statusCode") or 0)
    payload = parse_body(result.get("body"))
    rate_limit = payload.get("rate_limit") or payload.get("rateLimit") or {}
    rate_limit = rate_limit if isinstance(rate_limit, dict) else {}
    five_hour, weekly = classified_windows(rate_limit)
    weekly_used = used_percent(weekly)
    five_hour_used = used_percent(five_hour)

    if status_code == 401:
        return {"file": file_item.get("name"), "label": label, "action": "delete", "reason": "HTTP 401 invalid account"}
    if weekly_used is not None:
        if weekly_used >= settings.threshold:
            return {
                "file": file_item.get("name"),
                "label": label,
                "action": "keep" if disabled else "disable",
                "reason": f"weekly quota {weekly_used:.1f}% >= {settings.threshold:.1f}%",
                "used": weekly_used,
            }
        if disabled:
            return {
                "file": file_item.get("name"),
                "label": label,
                "action": "enable",
                "reason": f"weekly quota available ({weekly_used:.1f}%)",
                "used": weekly_used,
            }
        if five_hour_used is not None and five_hour_used >= settings.threshold:
            return {"file": file_item.get("name"), "label": label, "action": "keep", "reason": "5h quota full but weekly quota available", "used": weekly_used}
    return {"file": file_item.get("name"), "label": label, "action": "keep", "reason": "quota available", "used": weekly_used}


def set_disabled(settings: Settings, file_name: str, disabled: bool) -> None:
    if settings.dry_run:
        print(f"DRY-RUN {'disable' if disabled else 'enable'} {file_name}")
        return
    request_json(settings, "PATCH", "/auth-files/status", {"name": file_name, "disabled": disabled})


def main() -> int:
    settings = read_settings()
    response = request_json(settings, "GET", "/auth-files")
    files = response.get("files", []) if isinstance(response, dict) else []
    targets = [item for item in files if isinstance(item, dict) and normalize_provider(item) == settings.target_type]
    print(f"Codex inspection: total_files={len(files)} target_accounts={len(targets)} dry_run={settings.dry_run}")

    summary = {"delete": 0, "disable": 0, "enable": 0, "refresh": 0, "keep": 0, "failed": 0}
    for offset, item in enumerate(targets):
        try:
            result = inspect_account(settings, item)
            action = result["action"]
            summary[action] = summary.get(action, 0) + 1
            print(f"{result['label']} -> {action}: {result['reason']}")
            file_name = normalize_string(result.get("file"))
            record_history(settings, result, "issue")
            if action == "disable" and file_name:
                try:
                    set_disabled(settings, file_name, True)
                    record_history(settings, result, "execution", True)
                except Exception as exc:
                    record_history(settings, result, "execution", False, str(exc))
                    raise
            elif action == "enable" and file_name:
                try:
                    set_disabled(settings, file_name, False)
                    record_history(settings, result, "execution", True)
                except Exception as exc:
                    record_history(settings, result, "execution", False, str(exc))
                    raise
            elif action == "delete":
                print(f"skip delete {file_name}: automatic deletion is disabled")
            if maybe_refresh_disabled_token(settings, item, action):
                summary["refresh"] += 1
        except Exception as exc:
            summary["failed"] += 1
            print(f"ERROR {account_label(item)}: {exc}", file=sys.stderr)
        if settings.delay_seconds > 0 and offset < len(targets) - 1:
            time.sleep(settings.delay_seconds)

    print("Summary: " + json.dumps(summary, ensure_ascii=False, sort_keys=True))
    return 1 if summary["failed"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
