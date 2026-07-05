package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/dustin/go-humanize"
)

// ByteSize is a byte count parsed from plain integers or go-humanize size strings.
type ByteSize int64

func (b ByteSize) Int64() int64 {
	return int64(b)
}

func (b *ByteSize) Set(value string) error {
	parsed, err := ParseByteSize(value)
	if err != nil {
		return err
	}
	*b = ByteSize(parsed)
	return nil
}

func (b *ByteSize) UnmarshalText(text []byte) error {
	return b.Set(string(text))
}

func (b ByteSize) MarshalText() ([]byte, error) {
	return []byte(strconv.FormatInt(b.Int64(), 10)), nil
}

func (b *ByteSize) UnmarshalTOML(data any) error {
	switch value := data.(type) {
	case int64:
		*b = ByteSize(value)
		return nil
	case int:
		*b = ByteSize(value)
		return nil
	case int32:
		*b = ByteSize(value)
		return nil
	case uint64:
		if value > math.MaxInt64 {
			return fmt.Errorf("byte size %d overflows int64", value)
		}
		*b = ByteSize(value)
		return nil
	case float64:
		if value != math.Trunc(value) {
			return fmt.Errorf("byte size %v must be an integer", value)
		}
		if value > float64(math.MaxInt64) || value < float64(math.MinInt64) {
			return fmt.Errorf("byte size %v overflows int64", value)
		}
		*b = ByteSize(value)
		return nil
	case string:
		return b.Set(value)
	default:
		return fmt.Errorf("byte size must be an integer or string like 64MiB, not %T", data)
	}
}

// ParseByteSize parses a plain integer or a go-humanize byte size such as 64MiB or 42 MB.
func ParseByteSize(input string) (int64, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	if value, err := strconv.ParseInt(input, 10, 64); err == nil {
		return value, nil
	}
	parsed, err := humanize.ParseBytes(input)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", input, err)
	}
	if parsed > uint64(math.MaxInt64) {
		return 0, fmt.Errorf("byte size %q overflows int64", input)
	}
	return int64(parsed), nil
}
