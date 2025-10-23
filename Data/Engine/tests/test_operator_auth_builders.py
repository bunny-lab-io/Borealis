"""Tests for operator authentication builders."""

from __future__ import annotations

import pytest

from Data.Engine.builders import (
    OperatorLoginRequest,
    OperatorMFAVerificationRequest,
    build_login_request,
    build_mfa_request,
)


def test_build_login_request_uses_explicit_hash():
    payload = {"username": "Admin", "password_sha512": "abc123"}

    result = build_login_request(payload)

    assert isinstance(result, OperatorLoginRequest)
    assert result.username == "Admin"
    assert result.password_sha512 == "abc123"


def test_build_login_request_hashes_plain_password():
    payload = {"username": "user", "password": "secret"}

    result = build_login_request(payload)

    assert isinstance(result, OperatorLoginRequest)
    assert result.username == "user"
    assert result.password_sha512
    assert result.password_sha512 != "secret"


@pytest.mark.parametrize(
    "payload",
    [
        {"password": "secret"},
        {"username": ""},
        {"username": "user"},
    ],
)
def test_build_login_request_validation(payload):
    with pytest.raises(ValueError):
        build_login_request(payload)


def test_build_mfa_request_normalizes_code():
    payload = {"pending_token": "token", "code": "12 34-56"}

    result = build_mfa_request(payload)

    assert isinstance(result, OperatorMFAVerificationRequest)
    assert result.pending_token == "token"
    assert result.code == "123456"


def test_build_mfa_request_requires_token_and_code():
    with pytest.raises(ValueError):
        build_mfa_request({"code": "123"})
    with pytest.raises(ValueError):
        build_mfa_request({"pending_token": "token", "code": "12"})
