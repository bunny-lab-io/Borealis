# ======================================================
# Data\Engine\Unit_Tests\test_access_management_api.py
# Description: Validates access-management security hardening entrypoints.
#
# API Endpoints (if applicable): /api/auth/login, /api/auth/logout
# ======================================================

from __future__ import annotations

from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[3]


def test_webui_does_not_write_auth_cookie() -> None:
    webui_sources = [
        PROJECT_ROOT
        / "Data/Engine/Containers/webui-frontend/data/web-interface/src/Login.jsx",
        PROJECT_ROOT
        / "Data/Engine/Containers/webui-frontend/data/web-interface/src/app/runtime/bootstrapClientRuntime.js",
    ]

    for source_path in webui_sources:
        assert "document.cookie" not in source_path.read_text(encoding="utf-8")


def test_go_auth_cookies_are_secure_httponly() -> None:
    auth_source = (
        PROJECT_ROOT
        / "Data/Engine/Containers/api-backend/cmd/api-backend/auth_login.go"
    ).read_text(encoding="utf-8")
    logout_source = (
        PROJECT_ROOT / "Data/Engine/Containers/api-backend/cmd/api-backend/auth.go"
    ).read_text(encoding="utf-8")

    assert "HttpOnly: true" in auth_source
    assert "Secure:   true" in auth_source
    assert "SameSite: http.SameSiteLaxMode" in auth_source
    assert "HttpOnly: true" in logout_source
    assert "Secure:   true" in logout_source
    assert "SameSite: http.SameSiteLaxMode" in logout_source
