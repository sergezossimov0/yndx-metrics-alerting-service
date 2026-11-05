package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// Header is the HTTP header that carries the signature of the body.
const Header = "HashSHA256"

// Sum returns the base64-encoded (standard alphabet, padded) HMAC-SHA256 of
// data under key: 44 characters instead of 64 in hex. The agent and the
// server both sign and verify through this package, so they always agree on
// the encoding.
func Sum(data, key []byte) string {
	return base64.StdEncoding.EncodeToString(mac(data, key))
}

// Verify reports whether sig is the base64-encoded HMAC-SHA256 of data under
// key, as made by Sum. The comparison takes constant time, so response timing
// does not reveal how many leading bytes of a forged signature are correct.
func Verify(data, key []byte, sig string) bool {
	got, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(got, mac(data, key))
}

func mac(data, key []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data) // hash.Hash.Write never returns an error
	return h.Sum(nil)
}
