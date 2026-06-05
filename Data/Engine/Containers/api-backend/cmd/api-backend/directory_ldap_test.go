package main

import (
	"testing"
	"unicode/utf8"

	"github.com/go-ldap/ldap/v3"
)

func TestDirectoryEntryAttrsFormatsActiveDirectoryBinaryIDs(t *testing.T) {
	guidRaw := []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	sidRaw := []byte{
		0x01, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x05,
		0x15, 0x00, 0x00, 0x00,
		0x01, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00,
		0x03, 0x00, 0x00, 0x00,
	}
	entry := &ldap.Entry{
		DN: "CN=Operator,DC=example,DC=com",
		Attributes: []*ldap.EntryAttribute{
			{Name: "objectGUID", Values: []string{string(guidRaw)}, ByteValues: [][]byte{guidRaw}},
			{Name: "objectSid", Values: []string{string(sidRaw)}, ByteValues: [][]byte{sidRaw}},
		},
	}

	attrs := directoryEntryAttrs(entry)

	if got := firstDirectoryAttr(attrs, "objectGUID"); got != "00112233-4455-6677-8899-aabbccddeeff" {
		t.Fatalf("objectGUID = %q", got)
	}
	if got := firstDirectoryAttr(attrs, "objectSid"); got != "S-1-5-21-1-2-3" {
		t.Fatalf("objectSid = %q", got)
	}
	for name, values := range attrs {
		for _, value := range values {
			if !utf8.ValidString(value) {
				t.Fatalf("%s contains invalid UTF-8: %q", name, value)
			}
		}
	}
}

func TestDirectoryEntryAttrsEncodesUnknownBinaryValues(t *testing.T) {
	raw := []byte{0xf8, 0x00, 0xff}
	entry := &ldap.Entry{
		DN: "CN=Operator,DC=example,DC=com",
		Attributes: []*ldap.EntryAttribute{
			{Name: "binaryAttr", Values: []string{string(raw)}, ByteValues: [][]byte{raw}},
		},
	}

	attrs := directoryEntryAttrs(entry)

	if got := firstDirectoryAttr(attrs, "binaryAttr"); got != "hex:f800ff" {
		t.Fatalf("binaryAttr = %q", got)
	}
}
