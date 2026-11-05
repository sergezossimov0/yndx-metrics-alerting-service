package signature

import (
	"testing"
)

// RFC 4231, test case 2. rfcSum is the RFC's HMAC
// 5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843
// in base64; rfcHex is the same bytes in hex.
const (
	rfcKey  = "Jefe"
	rfcData = "what do ya want for nothing?"
	rfcSum  = "W9zBRr9gdU5qBCQmCJV1x1oAPwidJzmDnexYuWTsOEM="
	rfcHex  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

func TestSum_MatchesRFC4231Vector(t *testing.T) {
	got := Sum([]byte(rfcData), []byte(rfcKey))
	if got != rfcSum {
		t.Fatalf("expected %s, got %s", rfcSum, got)
	}
}

// 32 bytes of SHA256 are 44 characters in padded base64, against 64 in hex.
func TestSum_Is44Characters(t *testing.T) {
	if n := len(Sum([]byte(rfcData), []byte(rfcKey))); n != 44 {
		t.Fatalf("expected a 44-character signature, got %d", n)
	}
}

func TestVerify_AcceptsOwnSignature(t *testing.T) {
	data := []byte(`[{"id":"Alloc","type":"gauge","value":1.5}]`)
	key := []byte("secret")

	if !Verify(data, key, Sum(data, key)) {
		t.Fatal("expected signature to be valid")
	}
}

func TestVerify_Rejects(t *testing.T) {
	data := []byte(`[{"id":"Alloc","type":"gauge","value":1.5}]`)
	key := []byte("secret")
	sig := Sum(data, key)

	tests := []struct {
		name string
		data []byte
		key  []byte
		sig  string
	}{
		{name: "changed data", data: []byte(`[{"id":"Alloc","type":"gauge","value":2.5}]`), key: key, sig: sig},
		{name: "other key", data: data, key: []byte("other"), sig: sig},
		{name: "truncated signature", data: data, key: key, sig: sig[:len(sig)-4]},
		{name: "not base64", data: data, key: key, sig: "!!" + sig[2:]},
		{name: "empty signature", data: data, key: key, sig: ""},
		// the right HMAC in the old encoding: the format is base64 only
		{name: "hex encoding", data: []byte(rfcData), key: []byte(rfcKey), sig: rfcHex},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if Verify(tt.data, tt.key, tt.sig) {
				t.Fatalf("expected signature %q to be rejected", tt.sig)
			}
		})
	}
}
