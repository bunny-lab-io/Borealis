import unittest

from Data.Engine.builders.device_auth import (
    DeviceAuthRequestBuilder,
    RefreshTokenRequestBuilder,
)
from Data.Engine.domain.device_auth import DeviceAuthErrorCode, DeviceAuthFailure


class DeviceAuthRequestBuilderTests(unittest.TestCase):
    def test_build_successful_request(self) -> None:
        request = (
            DeviceAuthRequestBuilder()
            .with_authorization("Bearer abc123")
            .with_http_method("post")
            .with_htu("https://example.test/api")
            .with_service_context("currentUser")
            .with_dpop_proof("proof")
            .build()
        )

        self.assertEqual(request.access_token, "abc123")
        self.assertEqual(request.http_method, "POST")
        self.assertEqual(request.htu, "https://example.test/api")
        self.assertEqual(request.service_context, "CURRENTUSER")
        self.assertEqual(request.dpop_proof, "proof")

    def test_missing_authorization_raises_failure(self) -> None:
        builder = (
            DeviceAuthRequestBuilder()
            .with_http_method("GET")
            .with_htu("/health")
        )

        with self.assertRaises(DeviceAuthFailure) as ctx:
            builder.build()

        self.assertEqual(ctx.exception.code, DeviceAuthErrorCode.MISSING_AUTHORIZATION)


class RefreshTokenRequestBuilderTests(unittest.TestCase):
    def test_refresh_request_requires_all_fields(self) -> None:
        request = (
            RefreshTokenRequestBuilder()
            .with_payload({"guid": "de305d54-75b4-431b-adb2-eb6b9e546014", "refresh_token": "tok"})
            .with_http_method("post")
            .with_htu("https://example.test/api")
            .with_dpop_proof("proof")
            .build()
        )

        self.assertEqual(request.guid.value, "DE305D54-75B4-431B-ADB2-EB6B9E546014")
        self.assertEqual(request.refresh_token, "tok")
        self.assertEqual(request.http_method, "POST")
        self.assertEqual(request.htu, "https://example.test/api")
        self.assertEqual(request.dpop_proof, "proof")

    def test_refresh_request_missing_guid_raises_failure(self) -> None:
        builder = (
            RefreshTokenRequestBuilder()
            .with_payload({"refresh_token": "tok"})
            .with_http_method("POST")
            .with_htu("https://example.test/api")
        )

        with self.assertRaises(DeviceAuthFailure) as ctx:
            builder.build()

        self.assertEqual(ctx.exception.code, DeviceAuthErrorCode.INVALID_CLAIMS)
        self.assertIn("missing guid", ctx.exception.detail)


if __name__ == "__main__":  # pragma: no cover - convenience for local runs
    unittest.main()
