package utils_test

import (
	"testing"

	"vaps/internal/utils"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"1024", 1024},
		{"64MiB", 64 * 1024 * 1024},
		{"4 MiB", 4 * 1024 * 1024},
		{"42 MB", 42_000_000},
		{"512KiB", 512 * 1024},
		{"2M", 2_000_000},
	}

	for _, test := range tests {
		got, err := utils.ParseByteSize(test.input)
		if err != nil {
			t.Fatalf("ParseByteSize(%q) error = %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestByteSizeUnmarshalTOMLInteger(t *testing.T) {
	var size utils.ByteSize
	if err := size.UnmarshalTOML(int64(2048)); err != nil {
		t.Fatalf("UnmarshalTOML(int64) error = %v", err)
	}
	if size.Int64() != 2048 {
		t.Fatalf("UnmarshalTOML(int64) = %d, want 2048", size.Int64())
	}
}

func TestByteSizeUnmarshalTOMLString(t *testing.T) {
	var size utils.ByteSize
	if err := size.UnmarshalTOML("64MiB"); err != nil {
		t.Fatalf("UnmarshalTOML(string) error = %v", err)
	}
	if size.Int64() != 64*1024*1024 {
		t.Fatalf("UnmarshalTOML(string) = %d, want 64MiB", size.Int64())
	}
}

func TestIoHashSumHexUsesBLAKE3Leading160Bits(t *testing.T) {
	got := utils.IoHashSumHex([]byte("hello"))
	want := "ea8f163db38682925e4491c5e58d4bb3506ef8c1"
	if got != want {
		t.Fatalf("IoHashSumHex = %q, want %q", got, want)
	}
}

func TestIoHashValidAcceptsFortyHexCharacters(t *testing.T) {
	if !utils.IoHashValid("ea8f163db38682925e4491c5e58d4bb3506ef8c1") {
		t.Fatalf("IoHashValid returned false for BLAKE3-160 hex")
	}
	if utils.IoHashValid("ea8f163db38682925e4491c5e58d4bb3506ef8c") {
		t.Fatalf("IoHashValid returned true for short hash")
	}
}
