package signature

import (
	"strings"
	"testing"
)

// RFC 4231, test case 2.
const (
	rfcKey  = "Jefe"
	rfcData = "what do ya want for nothing?"
	rfcSum  = "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"
)

func TestSum_MatchesRFC4231Vector(t *testing.T) {
	got := Sum([]byte(rfcData), []byte(rfcKey))
	if got != rfcSum {
		t.Fatalf("expected %s, got %s", rfcSum, got)
	}
}

func TestVerify_AcceptsOwnSignature(t *testing.T) {
	data := []byte(`[{"id":"Alloc","type":"gauge","value":1.5}]`)
	key := []byte("secret")

	if !Verify(data, key, Sum(data, key)) {
		t.Fatal("expected signature to be valid")
	}
}

func TestVerify_AcceptsUpperCaseHex(t *testing.T) {
	if !Verify([]byte(rfcData), []byte(rfcKey), strings.ToUpper(rfcSum)) {
		t.Fatal("expected upper-case hex signature to be valid")
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
		{name: "truncated signature", data: data, key: key, sig: sig[:len(sig)-2]},
		{name: "not hex", data: data, key: key, sig: "zz" + sig[2:]},
		{name: "empty signature", data: data, key: key, sig: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if Verify(tt.data, tt.key, tt.sig) {
				t.Fatalf("expected signature %q to be rejected", tt.sig)
			}
		})
	}
}
