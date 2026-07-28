package objectstore

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	secretIDEnv  = "VAPS_STORAGE_S3_SECRETID"
	secretKeyEnv = "VAPS_STORAGE_S3_SECRETKEY"
)

type S3Credentials struct {
	SecretID  string
	SecretKey string
}

func LoadS3Credentials(baseDir string) (S3Credentials, error) {
	values := map[string]string{}
	for _, name := range []string{secretIDEnv, secretKeyEnv} {
		if value, ok := os.LookupEnv(name); ok {
			values[name] = value
		}
	}

	if err := loadDotEnv(filepath.Join(baseDir, ".env"), values); err != nil {
		return S3Credentials{}, err
	}

	credentials := S3Credentials{
		SecretID:  strings.TrimSpace(values[secretIDEnv]),
		SecretKey: strings.TrimSpace(values[secretKeyEnv]),
	}
	if credentials.SecretID == "" || credentials.SecretKey == "" {
		return S3Credentials{}, errors.New("VAPS_STORAGE_S3_SECRETID and VAPS_STORAGE_S3_SECRETKEY are required")
	}
	return credentials, nil
}

func loadDotEnv(path string, values map[string]string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("parse %s:%d: expected KEY=VALUE", path, lineNumber)
		}
		name = strings.TrimSpace(name)
		if name != secretIDEnv && name != secretKeyEnv {
			continue
		}
		parsed, err := parseDotEnvValue(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("parse %s:%d: %w", path, lineNumber, err)
		}
		values[name] = parsed
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

func parseDotEnvValue(value string) (string, error) {
	if len(value) < 2 {
		return value, nil
	}
	if value[0] == '"' {
		if value[len(value)-1] != '"' {
			return "", errors.New("unterminated double-quoted value")
		}
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value: %w", err)
		}
		return parsed, nil
	}
	if value[0] == '\'' {
		if value[len(value)-1] != '\'' {
			return "", errors.New("unterminated single-quoted value")
		}
		return value[1 : len(value)-1], nil
	}
	return value, nil
}
