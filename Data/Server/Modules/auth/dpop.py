"""
DPoP proof verification helpers.
"""

from __future__ import annotations

import hashlib
import time
from threading import Lock
from typing import Dict, Optional

import jwt

_DP0P_MAX_SKEW = 300.0  # seconds


class DPoPVerificationError(Exception):
    pass


class DPoPReplayError(DPoPVerificationError):
    pass


class DPoPValidator:
    def __init__(self) -> None:
        self._observed_jti: Dict[str, float] = {}
        self._lock = Lock()

    def verify(
        self,
        method: str,
        htu: str,
        proof: str,
        access_token: Optional[str] = None,
    ) -> str:
        """
        Verify the presented DPoP proof.  Returns the JWK thumbprint on success.
        """

        if not proof:
            raise DPoPVerificationError("DPoP proof missing")

        try:
            header = jwt.get_unverified_header(proof)
        except Exception as exc:
            raise DPoPVerificationError("invalid DPoP header") from exc

        jwk = header.get("jwk")
        alg = header.get("alg")
        if not jwk or not isinstance(jwk, dict):
            raise DPoPVerificationError("missing jwk in DPoP header")
        if alg not in ("EdDSA", "ES256", "ES384", "ES512"):
            raise DPoPVerificationError(f"unsupported DPoP alg {alg}")

        try:
            key = jwt.PyJWK(jwk)
            public_key = key.key
        except Exception as exc:
            raise DPoPVerificationError("invalid jwk in DPoP header") from exc

        try:
            claims = jwt.decode(
                proof,
                public_key,
                algorithms=[alg],
                options={"require": ["htm", "htu", "jti", "iat"]},
            )
        except Exception as exc:
            raise DPoPVerificationError("invalid DPoP signature") from exc

        htm = claims.get("htm")
        proof_htu = claims.get("htu")
        jti = claims.get("jti")
        iat = claims.get("iat")
        ath = claims.get("ath")

        if not isinstance(htm, str) or htm.lower() != method.lower():
            raise DPoPVerificationError("DPoP htm mismatch")
        if not isinstance(proof_htu, str) or proof_htu != htu:
            raise DPoPVerificationError("DPoP htu mismatch")
        if not isinstance(jti, str):
            raise DPoPVerificationError("DPoP jti missing")
        if not isinstance(iat, (int, float)):
            raise DPoPVerificationError("DPoP iat missing")

        now = time.time()
        if abs(now - float(iat)) > _DP0P_MAX_SKEW:
            raise DPoPVerificationError("DPoP proof outside allowed skew")

        if ath and access_token:
            expected_ath = jwt.utils.base64url_encode(
                hashlib.sha256(access_token.encode("utf-8")).digest()
            ).decode("ascii")
            if expected_ath != ath:
                raise DPoPVerificationError("DPoP ath mismatch")

        with self._lock:
            expiry = self._observed_jti.get(jti)
            if expiry and expiry > now:
                raise DPoPReplayError("DPoP proof replay detected")
            self._observed_jti[jti] = now + _DP0P_MAX_SKEW
            # Opportunistic cleanup
            stale = [key for key, exp in self._observed_jti.items() if exp <= now]
            for key in stale:
                self._observed_jti.pop(key, None)

        thumbprint = jwt.PyJWK(jwk).thumbprint()
        return thumbprint.decode("ascii")
