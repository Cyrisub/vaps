package appconfig

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"vaps/internal/utils"
)

//go:embed config.toml
var defaultConfigTOML []byte

type Duration time.Duration

type Config struct {
	Addr              string        `toml:"addr" hidden:"" name:"addr"`
	DataDir           string        `toml:"data_dir" hidden:"" name:"data-dir"`
	MetadataDB        string        `toml:"metadata_db" hidden:"" name:"metadata-db"`
	AuthDB            string        `toml:"auth_db" hidden:"" name:"auth-db"`
	UploadDB          string        `toml:"upload_db" hidden:"" name:"upload-db"`
	LogDir            string        `toml:"log_dir" hidden:"" name:"log-dir"`
	LogRetentionDays  int           `toml:"log_retention_days" hidden:"" name:"log-retention-days"`
	StatusLogInterval Duration      `toml:"status_log_interval" hidden:"" name:"status-log-interval"`
	Cache             CacheConfig   `toml:"cache" embed:"" prefix:"cache-"`
	Auth              AuthConfig    `toml:"auth" embed:"" prefix:"auth-"`
	Upload            UploadConfig  `toml:"upload" embed:"" prefix:"upload-"`
	Storage           StorageConfig `toml:"storage" embed:"" prefix:"storage-"`
}

type AuthConfig struct {
	ExpireTime Duration `toml:"expire_time" hidden:"" name:"expire-time"`
}

type UploadConfig struct {
	DirectMaxBytes  utils.ByteSize `toml:"direct_max_bytes" hidden:"" name:"direct-max-bytes"`
	Expiration      Duration       `toml:"expiration" hidden:"" name:"expiration"`
	CleanupInterval Duration       `toml:"cleanup_interval" hidden:"" name:"cleanup-interval"`
	Dir             string         `toml:"dir" hidden:"" name:"dir"`
}

type CacheConfig struct {
	Bytes          utils.ByteSize `toml:"bytes" hidden:"" name:"bytes"`
	MaxObjectBytes utils.ByteSize `toml:"max_object_bytes" hidden:"" name:"max-object-bytes"`
}

type StorageConfig struct {
	S3    S3StorageConfig    `toml:"s3" embed:"" prefix:"s3-"`
	Local LocalStorageConfig `toml:"local" embed:"" prefix:"local-"`
}

type S3StorageConfig struct {
	Bucket         string `toml:"bucket" hidden:"" name:"bucket"`
	Region         string `toml:"region" hidden:"" name:"region"`
	Endpoint       string `toml:"endpoint" hidden:"" name:"endpoint"`
	Prefix         string `toml:"prefix" hidden:"" name:"prefix"`
	ForcePathStyle bool   `toml:"force_path_style" hidden:"" name:"force-path-style"`
}

type LocalStorageConfig struct {
	Dir           string         `toml:"dir" hidden:"" name:"dir"`
	MaxCacheBytes utils.ByteSize `toml:"max_cache_bytes" hidden:"" name:"max-cache-bytes"`
}

func FromArgs(args []string) (Config, error) {
	if args == nil {
		args = os.Args[1:]
	}
	executable, err := os.Executable()
	if err != nil {
		return Config{}, err
	}
	return FromArgsWithBaseDir(args, filepath.Dir(executable))
}

func FromArgsWithBaseDir(args []string, baseDir string) (Config, error) {
	if printed, err := maybePrintVersion(args); err != nil {
		return Config{}, err
	} else if printed {
		return Config{}, ErrHelp
	}
	cfg, err := loadDefaultConfig()
	if err != nil {
		return Config{}, err
	}
	configPath, err := parseConfigPath(args)
	if err != nil {
		return Config{}, err
	}
	resolvedConfigPath := resolvePath(baseDir, configPath)
	configLoaded, err := configFileExists(resolvedConfigPath)
	if err != nil {
		return Config{}, err
	}
	if configLoaded {
		if err := loadTOMLConfig(resolvedConfigPath, &cfg); err != nil {
			return Config{}, err
		}
	}
	cfg, _, err = parseCLI(args, cfg, configPath)
	if err != nil {
		return Config{}, err
	}
	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	cfg.DataDir = resolvePath(baseDir, cfg.DataDir)
	cfg.LogDir = resolvePath(baseDir, cfg.LogDir)
	cfg.MetadataDB = resolvePath(baseDir, cfg.MetadataDB)
	cfg.AuthDB = resolvePath(baseDir, cfg.AuthDB)
	cfg.UploadDB = resolvePath(baseDir, cfg.UploadDB)
	cfg.Upload.Dir = resolvePath(baseDir, cfg.Upload.Dir)
	cfg.Storage.Local.Dir = resolvePath(baseDir, cfg.Storage.Local.Dir)
	return cfg, nil
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d *Duration) Set(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	return d.Set(string(text))
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func loadDefaultConfig() (Config, error) {
	var cfg Config
	if err := decodeTOML(defaultConfigTOML, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse embedded default config: %w", err)
	}
	return cfg, nil
}

func decodeTOML(data []byte, cfg *Config) error {
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(cfg)
	if err != nil {
		return err
	}
	return rejectUnknownFields(md)
}

func loadTOMLConfig(path string, cfg *Config) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	md, err := toml.NewDecoder(file).Decode(cfg)
	if err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := rejectUnknownFields(md); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func rejectUnknownFields(md toml.MetaData) error {
	undecoded := md.Undecoded()
	if len(undecoded) == 0 {
		return nil
	}
	return fmt.Errorf("unknown field %q", undecoded[0].String())
}

func normalize(cfg *Config) {
	cfg.Storage.S3.Bucket = strings.TrimSpace(cfg.Storage.S3.Bucket)
	cfg.Storage.S3.Region = strings.TrimSpace(cfg.Storage.S3.Region)
	cfg.Storage.S3.Endpoint = strings.TrimSpace(cfg.Storage.S3.Endpoint)
	cfg.Storage.S3.Prefix = strings.Trim(cfg.Storage.S3.Prefix, "/")
}

func validate(cfg Config) error {
	if time.Duration(cfg.Auth.ExpireTime) <= 0 {
		return errors.New("auth-expire-time must be positive")
	}
	if cfg.Cache.Bytes < 0 {
		return errors.New("cache-bytes must not be negative")
	}
	if cfg.Cache.MaxObjectBytes < 0 {
		return errors.New("cache-max-object-bytes must not be negative")
	}
	if cfg.Upload.DirectMaxBytes <= 0 {
		return errors.New("upload-direct-max-bytes must be positive")
	}
	if time.Duration(cfg.Upload.Expiration) <= 0 {
		return errors.New("upload-expiration must be positive")
	}
	if time.Duration(cfg.Upload.CleanupInterval) <= 0 {
		return errors.New("upload-cleanup-interval must be positive")
	}
	if cfg.Storage.S3.Bucket == "" {
		return errors.New("storage-s3-bucket is required")
	}
	if cfg.Storage.S3.Region == "" {
		return errors.New("storage-s3-region is required")
	}
	if endpoint := cfg.Storage.S3.Endpoint; endpoint == "" {
		return errors.New("storage-s3-endpoint is required")
	} else {
		parsed, err := url.Parse(endpoint)
		if err != nil ||
			!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") ||
			parsed.Hostname() == "" ||
			parsed.User != nil ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return errors.New("storage-s3-endpoint must be a valid http(s) URL")
		}
	}
	if cfg.Storage.Local.Dir == "" {
		return errors.New("storage-local-dir is required")
	}
	if cfg.Storage.Local.MaxCacheBytes < 0 {
		return errors.New("storage-local-max-cache-bytes must not be negative")
	}
	return nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
