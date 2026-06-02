package main

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

func verifyTOTPCode(secret, code string, now time.Time) bool {
	digits := onlyDigits(code)
	if len(digits) < 6 {
		return false
	}
	digits = digits[:6]
	step := now.Unix() / 30
	for offset := int64(-1); offset <= 1; offset++ {
		candidate := step + offset
		if candidate < 0 {
			continue
		}
		if hotp(secret, uint64(candidate)) == digits {
			return true
		}
	}
	return false
}

func hotp(secret string, counter uint64) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil {
		key, err = base32.StdEncoding.DecodeString(normalized)
		if err != nil {
			return ""
		}
	}
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counterBytes[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", value%1000000)
}

func totpProvisioningURI(secret, username string) string {
	issuer := strings.TrimSpace(os.Getenv("BOREALIS_MFA_ISSUER"))
	if issuer == "" {
		issuer = "Borealis"
	}
	values := url.Values{}
	values.Set("secret", strings.TrimSpace(secret))
	values.Set("issuer", issuer)
	return "otpauth://totp/" + url.PathEscape(issuer+":"+firstText(username, "user")) + "?" + values.Encode()
}

func onlyDigits(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if char >= '0' && char <= '9' {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
