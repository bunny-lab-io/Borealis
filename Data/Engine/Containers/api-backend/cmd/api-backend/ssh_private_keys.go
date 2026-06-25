package main

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

func normalizeSSHPrivateKeyMaterial(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `\r\n`, "\n")
	value = strings.ReplaceAll(value, `\n`, "\n")
	value = strings.ReplaceAll(value, `\r`, "\n")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if value != "" && !strings.HasSuffix(value, "\n") {
		value += "\n"
	}
	return value
}

func parseSSHPrivateKeyMaterial(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := ssh.ParseRawPrivateKey([]byte(value)); err != nil {
		return fmt.Errorf("ssh_private_key_parse_failed: %w", err)
	}
	return nil
}
