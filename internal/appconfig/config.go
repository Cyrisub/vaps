package appconfig

import (
	"bytes"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"vaps/internal/utils"
)

//go:embed config.toml
var defaultConfigTOML []byte

type Duration time.Duration

type Config struct {
	Addr              string       `toml:"addr"`
	DataDir           string       `toml:"data_dir"`
	MetadataDB        string       `toml:"metadata_db"`
	AuthDB            string       `toml:"auth_db"`
	UploadDB          string       `toml:"upload_db"`
	LogDir            string       `toml:"log_dir"`
	LogRetentionDays  int          `toml:"log_retention_days"`
	StatusLogInterval Duration     `toml:"status_log_interval"`
	Cache             CacheConfig  `toml:"cache"`
	Auth              AuthConfig   `toml:"auth"`
	Upload            UploadConfig `toml:"upload"`
	Backup            BackupConfig `toml:"backup"`
}

type AuthConfig struct {
	ExpireTime Duration `toml:"expire_time"`
}

type UploadConfig struct {
	DirectMaxBytes  utils.ByteSize `toml:"direct_max_bytes"`
	Expiration      Duration       `toml:"expiration"`
	CleanupInterval Duration       `toml:"cleanup_interval"`
	Dir             string         `toml:"dir"`
}

type CacheConfig struct {
	Bytes          utils.ByteSize `toml:"bytes"`
	MaxObjectBytes utils.ByteSize `toml:"max_object_bytes"`
}

type BackupConfig struct {
	Backend       []string        `toml:"backend"`
	FlushInterval Duration        `toml:"flush_interval"`
	MaxPending    int             `toml:"max_pending"`
	SVN           SVNBackupConfig `toml:"svn"`
}

type SVNBackupConfig struct {
	URL     string `toml:"url"`
	Bin     string `toml:"bin"`
	MuccBin string `toml:"mucc_bin" flag_alias:"backup-svnmucc-bin"`
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
	cfg, err := loadDefaultConfig()
	if err != nil {
		return Config{}, err
	}
	configPath, err := findConfigPath(args)
	if err != nil {
		return Config{}, err
	}
	if configPath != "" {
		if err := loadTOMLConfig(resolvePath(baseDir, configPath), &cfg); err != nil {
			return Config{}, err
		}
	}
	flags := flag.NewFlagSet("vaps", flag.ContinueOnError)
	flags.String("config", configPath, "TOML config file path")
	if err := registerFlags(flags, &cfg); err != nil {
		return Config{}, err
	}
	if err := flags.Parse(args); err != nil {
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

func findConfigPath(args []string) (string, error) {
	for index, arg := range args {
		if arg == "-config" || arg == "--config" {
			if index+1 >= len(args) {
				return "", errors.New("config flag requires a value")
			}
			return args[index+1], nil
		}
		if strings.HasPrefix(arg, "-config=") {
			return strings.TrimPrefix(arg, "-config="), nil
		}
		if strings.HasPrefix(arg, "--config=") {
			return strings.TrimPrefix(arg, "--config="), nil
		}
	}
	return "", nil
}

func registerFlags(flags *flag.FlagSet, cfg *Config) error {
	return registerValueFlags(flags, nil, reflect.ValueOf(cfg).Elem())
}

func registerValueFlags(flags *flag.FlagSet, prefix []string, value reflect.Value) error {
	valueType := value.Type()
	if valueType == reflect.TypeOf(Duration(0)) || valueType == reflect.TypeOf(utils.ByteSize(0)) || value.Kind() != reflect.Struct {
		registerFlag(flags, strings.Join(prefix, "-"), "override "+strings.Join(prefix, "."), value)
		return nil
	}
	for index := 0; index < value.NumField(); index++ {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		name := configFieldName(field)
		if name == "" {
			continue
		}
		child := value.Field(index)
		childPrefix := append(prefix, strings.ReplaceAll(name, "_", "-"))
		if err := registerValueFlags(flags, childPrefix, child); err != nil {
			return err
		}
		for _, alias := range splitAliases(field.Tag.Get("flag_alias")) {
			registerFlag(flags, alias, "override "+strings.Join(childPrefix, "."), child)
		}
	}
	return nil
}

func registerFlag(flags *flag.FlagSet, name, usage string, value reflect.Value) {
	if value.Type() == reflect.TypeOf(Duration(0)) {
		flags.Var(durationFlag{value: value}, name, usage)
		return
	}
	if value.Type() == reflect.TypeOf(utils.ByteSize(0)) {
		flags.Var(byteSizeFlag{value: value}, name, usage)
		return
	}
	if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.String {
		flags.Var(stringSliceFlag{value: value}, name, usage)
		return
	}
	flags.Var(scalarFlag{value: value}, name, usage)
}

func splitAliases(value string) []string {
	aliases := []string{}
	for _, alias := range strings.Split(value, ",") {
		alias = strings.TrimSpace(alias)
		if alias != "" {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func configFieldName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("toml"), ",")[0]
	if name == "-" {
		return ""
	}
	if name != "" {
		return name
	}
	return strings.ToLower(field.Name)
}

type byteSizeFlag struct {
	value reflect.Value
}

func (f byteSizeFlag) String() string {
	return strconv.FormatInt(f.value.Int(), 10)
}

func (f byteSizeFlag) Set(value string) error {
	var size utils.ByteSize
	if err := size.Set(value); err != nil {
		return err
	}
	f.value.SetInt(size.Int64())
	return nil
}

type stringSliceFlag struct {
	value reflect.Value
}

func (f stringSliceFlag) String() string {
	if f.value.Len() == 0 {
		return ""
	}
	parts := make([]string, f.value.Len())
	for index := 0; index < f.value.Len(); index++ {
		parts[index] = f.value.Index(index).String()
	}
	return strings.Join(parts, ",")
}

func (f stringSliceFlag) Set(value string) error {
	parts := splitFlagList(value)
	slice := reflect.MakeSlice(f.value.Type(), len(parts), len(parts))
	for index, part := range parts {
		slice.Index(index).SetString(part)
	}
	f.value.Set(slice)
	return nil
}

func splitFlagList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

type durationFlag struct {
	value reflect.Value
}

func (f durationFlag) String() string {
	return time.Duration(f.value.Int()).String()
}

func (f durationFlag) Set(value string) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	f.value.SetInt(int64(parsed))
	return nil
}

type scalarFlag struct {
	value reflect.Value
}

func (f scalarFlag) String() string {
	switch f.value.Kind() {
	case reflect.String:
		return f.value.String()
	case reflect.Int, reflect.Int64:
		return strconv.FormatInt(f.value.Int(), 10)
	default:
		return ""
	}
}

func (f scalarFlag) Set(value string) error {
	switch f.value.Kind() {
	case reflect.String:
		f.value.SetString(value)
		return nil
	case reflect.Int:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 0)
		if err != nil {
			return err
		}
		f.value.SetInt(parsed)
		return nil
	case reflect.Int64:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return err
		}
		f.value.SetInt(parsed)
		return nil
	default:
		return fmt.Errorf("unsupported config field type %s", f.value.Type())
	}
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
