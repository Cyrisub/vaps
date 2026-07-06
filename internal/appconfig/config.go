package appconfig

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
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
	Addr              string       `toml:"addr" hidden:"" name:"addr"`
	DataDir           string       `toml:"data_dir" hidden:"" name:"data-dir"`
	MetadataDB        string       `toml:"metadata_db" hidden:"" name:"metadata-db"`
	AuthDB            string       `toml:"auth_db" hidden:"" name:"auth-db"`
	UploadDB          string       `toml:"upload_db" hidden:"" name:"upload-db"`
	LogDir            string       `toml:"log_dir" hidden:"" name:"log-dir"`
	LogRetentionDays  int          `toml:"log_retention_days" hidden:"" name:"log-retention-days"`
	StatusLogInterval Duration     `toml:"status_log_interval" hidden:"" name:"status-log-interval"`
	Cache             CacheConfig  `toml:"cache" embed:"" prefix:"cache-"`
	Auth              AuthConfig   `toml:"auth" embed:"" prefix:"auth-"`
	Upload            UploadConfig `toml:"upload" embed:"" prefix:"upload-"`
	Backup            BackupConfig `toml:"backup" embed:"" prefix:"backup-"`
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

type BackupConfig struct {
	Backend       []string        `toml:"backend" hidden:"" name:"backend"`
	FlushInterval Duration        `toml:"flush_interval" hidden:"" name:"flush-interval"`
	MaxPending    int             `toml:"max_pending" hidden:"" name:"max-pending"`
	SVN           SVNBackupConfig `toml:"svn" embed:"" prefix:"svn-"`
}

type SVNBackupConfig struct {
	URL     string `toml:"url" hidden:"" name:"url"`
	Bin     string `toml:"bin" hidden:"" name:"bin"`
	MuccBin string `toml:"mucc_bin" hidden:"" name:"mucc-bin" aliases:"backup-svnmucc-bin"`
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
	cfg.Backup.Backend = normalizeBackupBackends(cfg.Backup.Backend)
	cfg.Backup.SVN.URL = strings.TrimSpace(cfg.Backup.SVN.URL)
	cfg.Backup.SVN.Bin = strings.TrimSpace(cfg.Backup.SVN.Bin)
	cfg.Backup.SVN.MuccBin = strings.TrimSpace(cfg.Backup.SVN.MuccBin)
}

func normalizeBackupBackends(backends []string) []string {
	normalized := make([]string, 0, len(backends))
	for _, name := range backends {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		normalized = append(normalized, name)
	}
	return normalized
}

// EnabledBackupBackends returns configured backup backends excluding none entries.
func EnabledBackupBackends(backends []string) []string {
	enabled := make([]string, 0, len(backends))
	for _, name := range backends {
		if name != "none" {
			enabled = append(enabled, name)
		}
	}
	return enabled
}

func validate(cfg Config) error {
	if time.Duration(cfg.Auth.ExpireTime) <= 0 {
		return errors.New("auth-expire-time must be positive")
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
	if time.Duration(cfg.Backup.FlushInterval) < 0 {
		return errors.New("backup-flush-interval must not be negative")
	}
	if cfg.Backup.MaxPending < 0 {
		return errors.New("backup-max-pending must not be negative")
	}
	return validateBackupBackends(cfg.Backup.Backend, cfg.Backup.SVN)
}

func validateBackupBackends(backends []string, svn SVNBackupConfig) error {
	seen := map[string]struct{}{}
	svnEnabled := false
	for _, name := range backends {
		if name == "none" {
			continue
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("duplicate backup backend %q", name)
		}
		seen[name] = struct{}{}
		switch name {
		case "svn":
			svnEnabled = true
		default:
			return errors.New("backup backend must be one of none, svn")
		}
	}
	if !svnEnabled {
		return nil
	}
	if svn.URL == "" {
		return errors.New("backup-svn-url is required when backup backend is svn")
	}
	if svn.Bin == "" {
		return errors.New("backup-svn-bin must not be empty when backup backend is svn")
	}
	if svn.MuccBin == "" {
		return errors.New("backup-svnmucc-bin must not be empty when backup backend is svn")
	}
	return nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
