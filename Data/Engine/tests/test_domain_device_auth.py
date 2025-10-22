import unittest

from Data.Engine.domain.device_auth import (
    DeviceAuthErrorCode,
    DeviceAuthFailure,
    DeviceFingerprint,
    DeviceGuid,
    sanitize_service_context,
)


class DeviceGuidTests(unittest.TestCase):
    def test_guid_normalization_accepts_braces_and_lowercase(self) -> None:
        guid = DeviceGuid("{de305d54-75b4-431b-adb2-eb6b9e546014}")
        self.assertEqual(guid.value, "DE305D54-75B4-431B-ADB2-EB6B9E546014")

    def test_guid_rejects_empty_string(self) -> None:
        with self.assertRaises(ValueError):
            DeviceGuid("")


class DeviceFingerprintTests(unittest.TestCase):
    def test_fingerprint_normalization_trims_and_lowercases(self) -> None:
        fingerprint = DeviceFingerprint("  AA:BB:CC  ")
        self.assertEqual(fingerprint.value, "aa:bb:cc")

    def test_fingerprint_rejects_blank_input(self) -> None:
        with self.assertRaises(ValueError):
            DeviceFingerprint("   ")


class ServiceContextTests(unittest.TestCase):
    def test_sanitize_service_context_returns_uppercase_only(self) -> None:
        self.assertEqual(sanitize_service_context("system"), "SYSTEM")

    def test_sanitize_service_context_filters_invalid_chars(self) -> None:
        self.assertEqual(sanitize_service_context("sys tem!"), "SYSTEM")

    def test_sanitize_service_context_returns_none_for_empty_result(self) -> None:
        self.assertIsNone(sanitize_service_context("@@@"))


class DeviceAuthFailureTests(unittest.TestCase):
    def test_to_dict_includes_retry_after_and_detail(self) -> None:
        failure = DeviceAuthFailure(
            DeviceAuthErrorCode.RATE_LIMITED,
            http_status=429,
            retry_after=30,
            detail="too many attempts",
        )
        payload = failure.to_dict()
        self.assertEqual(
            payload,
            {"error": "rate_limited", "retry_after": 30.0, "detail": "too many attempts"},
        )


if __name__ == "__main__":  # pragma: no cover - convenience for local runs
    unittest.main()
