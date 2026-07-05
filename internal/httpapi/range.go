package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type byteRange struct {
	start int64
	end   int64
}

func parseSingleRangeHeader(value string, size int64) (byteRange, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes=") {
		return byteRange{}, errors.New("unsupported range unit")
	}
	spec := strings.TrimPrefix(value, "bytes=")
	if strings.Contains(spec, ",") {
		return byteRange{}, errors.New("multipart ranges are not supported")
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return byteRange{}, errors.New("invalid range")
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return byteRange{}, errors.New("invalid range")
		}
		if suffix > size {
			suffix = size
		}
		return byteRange{start: size - suffix, end: size - 1}, nil
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return byteRange{}, errors.New("invalid range")
	}
	if parts[1] == "" {
		if start >= size {
			return byteRange{}, errRangeNotSatisfiable
		}
		return byteRange{start: start, end: size - 1}, nil
	}
	end, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return byteRange{}, errors.New("invalid range")
	}
	if start >= size {
		return byteRange{}, errRangeNotSatisfiable
	}
	if end >= size {
		end = size - 1
	}
	return byteRange{start: start, end: end}, nil
}

var errRangeNotSatisfiable = errors.New("range not satisfiable")

func (r byteRange) length() int64 {
	return r.end - r.start + 1
}

func writeRangeResponse(w http.ResponseWriter, data []byte, infoSize int64, parsed byteRange) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", parsed.start, parsed.end, infoSize))
	w.Header().Set("Content-Length", strconv.FormatInt(parsed.length(), 10))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(data[parsed.start : parsed.end+1])
}

func writeFullPayload(w http.ResponseWriter, data []byte, infoSize int64) {
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Length", strconv.FormatInt(infoSize, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func serveRangeFromReader(w http.ResponseWriter, reader io.ReadSeeker, infoSize int64, rangeHeader string) error {
	w.Header().Set("Accept-Ranges", "bytes")
	if rangeHeader == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(infoSize, 10))
		w.WriteHeader(http.StatusOK)
		_, err := io.Copy(w, reader)
		return err
	}
	parsed, err := parseSingleRangeHeader(rangeHeader, infoSize)
	if err != nil {
		if errors.Is(err, errRangeNotSatisfiable) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", infoSize))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return nil
		}
		return err
	}
	if _, err := reader.Seek(parsed.start, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", parsed.start, parsed.end, infoSize))
	w.Header().Set("Content-Length", strconv.FormatInt(parsed.length(), 10))
	w.WriteHeader(http.StatusPartialContent)
	_, err = io.CopyN(w, reader, parsed.length())
	return err
}
