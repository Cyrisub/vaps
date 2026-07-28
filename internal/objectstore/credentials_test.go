package objectstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadS3CredentialsUsesProcessEnvironment(t *testing.T) {
	t.Setenv(secretIDEnv, "process-secret-id")
	t.Setenv(secretKeyEnv, "process-secret-key")

	credentials, err := LoadS3Credentials(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if credentials.SecretID != "process-secret-id" || credentials.SecretKey != "process-secret-key" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestLoadS3CredentialsDotEnvOverridesProcessEnvironment(t *testing.T) {
	t.Setenv(secretIDEnv, "process-secret-id")
	t.Setenv(secretKeyEnv, "process-secret-key")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"VAPS_STORAGE_S3_SECRETID=\"dotenv-secret-id\"\n"+
			"export VAPS_STORAGE_S3_SECRETKEY='dotenv-secret-key'\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}

	credentials, err := LoadS3Credentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if credentials.SecretID != "dotenv-secret-id" || credentials.SecretKey != "dotenv-secret-key" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestLoadS3CredentialsRejectsMissingValues(t *testing.T) {
	t.Setenv(secretIDEnv, "")
	t.Setenv(secretKeyEnv, "")

	if _, err := LoadS3Credentials(t.TempDir()); err == nil {
		t.Fatal("LoadS3Credentials() error = nil, want missing credentials error")
	}
}
