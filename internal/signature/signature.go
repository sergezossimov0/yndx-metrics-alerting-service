package signature

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Header is the HTTP header that carries the signature of the body.
const Header = "HashSHA256"

// Sum returns the hex-encoded HMAC-SHA256 of data under key.
func Sum(data, key []byte) string {
	return hex.EncodeToString(mac(data, key))
}

// Verify reports whether sig is the hex-encoded HMAC-SHA256 of data under key.
// The comparison takes constant time, so response timing does not reveal how
// many leading bytes of a forged signature are correct.
func Verify(data, key []byte, sig string) bool {
	got, err := hex.DecodeString(sig)
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
