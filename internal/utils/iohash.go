package utils

import (
	"encoding/hex"
	"hash"
	"hash/crc32"
	"strings"

	"github.com/zeebo/blake3"
)

const (
	IoHashSize      = 20
	IoHashHexSize   = IoHashSize * 2
	ChecksumSize    = crc32.Size
	ChecksumHexSize = ChecksumSize * 2
)

func NewIoHash() hash.Hash {
	return blake3.New()
}

func IoHashSumHex(payload []byte) string {
	sum := blake3.Sum256(payload)
	return hex.EncodeToString(sum[:IoHashSize])
}

func IoHashDigestHex(hasher hash.Hash) string {
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum[:IoHashSize])
}

// Blake3DigestHex returns the full BLAKE3-256 digest. FIoHash is intentionally
// truncated for Unreal compatibility; callers that establish persistence use
// this full digest to detect the otherwise theoretical FIoHash collision.
func Blake3DigestHex(hasher hash.Hash) string {
	return hex.EncodeToString(hasher.Sum(nil))
}

func NewChecksum() hash.Hash {
	return crc32.New(crc32.MakeTable(crc32.Castagnoli))
}

func ChecksumDigestHex(hasher hash.Hash) string {
	sum := hasher.Sum(nil)
	return hex.EncodeToString(sum)
}

func ChecksumValid(value string) bool {
	if len(value) != ChecksumHexSize {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func IoHashValid(value string) bool {
	if len(value) != IoHashHexSize {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
