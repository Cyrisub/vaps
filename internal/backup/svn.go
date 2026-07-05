package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"vaps/internal/blobstore"
	"vaps/internal/utils"
)

var ErrBackupHashMismatch = errors.New("backup payload hash does not match requested iohash")

type CommandResult struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
}

type SVNConfig struct {
	URL        string
	SVNBin     string
	SVNMuccBin string
	TempDir    string
	Username   string
	Password   string
	Runner     CommandRunner
}

type SVNBackend struct {
	url        string
	svnBin     string
	svnmuccBin string
	tempDir    string
	username   string
	password   string
	runner     CommandRunner
}

type execCommandRunner struct{}

func NewSVNBackend(cfg SVNConfig) (*SVNBackend, error) {
	url := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if url == "" {
		return nil, errors.New("svn backup url must not be empty")
	}
	svnBin := cfg.SVNBin
	if svnBin == "" {
		svnBin = "svn"
	}
	svnmuccBin := cfg.SVNMuccBin
	if svnmuccBin == "" {
		svnmuccBin = "svnmucc"
	}
	runner := cfg.Runner
	if runner == nil {
		runner = execCommandRunner{}
	}
	log.Printf(
		"svn backup initialized url=%q svn_bin=%q svnmucc_bin=%q temp_dir=%q username_configured=%t password_configured=%t",
		url,
		svnBin,
		svnmuccBin,
		cfg.TempDir,
		cfg.Username != "",
		cfg.Password != "",
	)
	return &SVNBackend{
		url:        url,
		svnBin:     svnBin,
		svnmuccBin: svnmuccBin,
		tempDir:    cfg.TempDir,
		username:   cfg.Username,
		password:   cfg.Password,
		runner:     runner,
	}, nil
}

func (s *SVNBackend) Name() string {
	return "svn"
}

func (s *SVNBackend) Exists(ctx context.Context, hash string) (bool, error) {
	rel, err := payloadPath(hash)
	if err != nil {
		return false, err
	}
	exists, err := s.urlExists(ctx, joinURL(s.url, rel))
	if err != nil {
		log.Printf("svn backup exists failed hash=%s path=%q error=%q", hash, rel, err)
		return false, err
	}
	log.Printf("svn backup exists checked hash=%s path=%q exists=%t", hash, rel, exists)
	return exists, nil
}

func (s *SVNBackend) Open(ctx context.Context, hash string) (io.ReadCloser, error) {
	rel, err := payloadPath(hash)
	if err != nil {
		return nil, err
	}
	result, err := s.runner.Run(ctx, s.svnBin, s.svnArgs("cat", joinURL(s.url, rel))...)
	if err != nil {
		return nil, commandError("svn cat", result, err)
	}
	return io.NopCloser(strings.NewReader(result.Stdout)), nil
}

func (s *SVNBackend) Put(ctx context.Context, hash string, reader io.Reader) (Object, error) {
	objects, err := s.PutBatch(ctx, []Payload{{Hash: hash, Reader: reader}})
	if err != nil {
		return Object{}, err
	}
	if len(objects) == 0 {
		return Object{}, errors.New("svn backup put produced no object")
	}
	return objects[0], nil
}

func (s *SVNBackend) PutBatch(ctx context.Context, payloads []Payload) ([]Object, error) {
	started := time.Now()
	log.Printf("svn backup batch started count=%d", len(payloads))
	prepared := make([]preparedSVNPayload, 0, len(payloads))
	objects := make([]Object, 0, len(payloads))
	for _, payload := range payloads {
		item, err := s.preparePayload(ctx, payload)
		if err != nil {
			s.cleanupPrepared(prepared)
			log.Printf("svn backup batch failed stage=prepare hash=%s error=%q", payload.Hash, err)
			return nil, err
		}
		objects = append(objects, item.object)
		if item.tmpPath != "" {
			prepared = append(prepared, item)
		}
	}
	defer s.cleanupPrepared(prepared)
	if len(prepared) == 0 {
		log.Printf("svn backup batch skipped_existing count=%d duration=%s", len(objects), time.Since(started).Truncate(time.Millisecond))
		return objects, nil
	}

	dirs, err := s.missingDirs(ctx, prepared)
	if err != nil {
		log.Printf("svn backup batch failed stage=missing_dirs error=%q", err)
		return nil, err
	}
	actions := make([]string, 0, len(dirs)*2+len(prepared)*3)
	for _, dir := range dirs {
		actions = append(actions, "mkdir", joinURL(s.url, dir))
	}
	for _, item := range prepared {
		actions = append(actions, "put", item.tmpPath, item.targetURL)
	}

	message := fmt.Sprintf("backup: store %d payloads", len(prepared))
	result, err := s.runner.Run(ctx, s.svnmuccBin, s.svnmuccArgs(message, actions...)...)
	if err != nil {
		err = commandError("svnmucc batch put", result, err)
		log.Printf("svn backup batch failed stage=svnmucc count=%d error=%q", len(prepared), err)
		return nil, err
	}
	log.Printf("svn backup batch completed count=%d stored=%d dirs=%d duration=%s", len(payloads), len(prepared), len(dirs), time.Since(started).Truncate(time.Millisecond))
	return objects, nil
}

type preparedSVNPayload struct {
	object    Object
	tmpPath   string
	targetURL string
}

func (s *SVNBackend) preparePayload(ctx context.Context, payload Payload) (preparedSVNPayload, error) {
	rel, err := payloadPath(payload.Hash)
	if err != nil {
		return preparedSVNPayload{}, err
	}
	object := Object{Hash: payload.Hash, Path: rel}
	tmpPath, err := s.writeTemp(payload.Hash, payload.Reader)
	if err != nil {
		return preparedSVNPayload{}, err
	}
	targetURL := joinURL(s.url, rel)
	exists, err := s.urlExists(ctx, targetURL)
	if err != nil {
		_ = os.Remove(tmpPath)
		return preparedSVNPayload{}, err
	}
	if exists {
		_ = os.Remove(tmpPath)
		log.Printf("svn backup batch skipped_existing hash=%s path=%q", payload.Hash, rel)
		return preparedSVNPayload{object: object}, nil
	}
	return preparedSVNPayload{object: object, tmpPath: tmpPath, targetURL: targetURL}, nil
}

func (s *SVNBackend) missingDirs(ctx context.Context, payloads []preparedSVNPayload) ([]string, error) {
	seen := map[string]struct{}{}
	missing := []string{}
	for _, payload := range payloads {
		for _, dir := range payloadDirs(payload.object.Hash) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			exists, err := s.urlExists(ctx, joinURL(s.url, dir))
			if err != nil {
				return nil, err
			}
			if !exists {
				missing = append(missing, dir)
			}
		}
	}
	return missing, nil
}

func (s *SVNBackend) cleanupPrepared(payloads []preparedSVNPayload) {
	for _, payload := range payloads {
		if payload.tmpPath != "" {
			_ = os.Remove(payload.tmpPath)
		}
	}
}

func (s *SVNBackend) List(ctx context.Context) ([]Object, error) {
	started := time.Now()
	rootURL := s.url
	log.Printf("svn backup list started url=%q", rootURL)
	result, err := s.runner.Run(ctx, s.svnBin, s.svnArgs("list", "-R", rootURL)...)
	if err != nil {
		err = commandError("svn list", result, err)
		log.Printf("svn backup list failed url=%q error=%q", rootURL, err)
		return nil, err
	}

	seen := map[string]struct{}{}
	objects := []Object{}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		object, ok := parsePayloadListEntry(line)
		if !ok {
			continue
		}
		if _, exists := seen[object.Hash]; exists {
			continue
		}
		seen[object.Hash] = struct{}{}
		objects = append(objects, object)
	}
	log.Printf("svn backup list completed url=%q count=%d duration=%s", rootURL, len(objects), time.Since(started).Truncate(time.Millisecond))
	return objects, nil
}

func (s *SVNBackend) ensureDir(ctx context.Context, rel string) error {
	dirURL := joinURL(s.url, rel)
	exists, err := s.urlExists(ctx, dirURL)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	log.Printf("svn backup mkdir started path=%q", rel)
	result, err := s.runner.Run(ctx, s.svnmuccBin, s.svnmuccArgs("backup: create directory "+rel, "mkdir", dirURL)...)
	if err != nil {
		if isAlreadyExists(result, err) {
			log.Printf("svn backup mkdir skipped_existing path=%q", rel)
			return nil
		}
		err = commandError("svnmucc mkdir", result, err)
		log.Printf("svn backup mkdir failed path=%q error=%q", rel, err)
		return err
	}
	log.Printf("svn backup mkdir completed path=%q", rel)
	return nil
}

func (s *SVNBackend) urlExists(ctx context.Context, url string) (bool, error) {
	result, err := s.runner.Run(ctx, s.svnBin, s.svnArgs("info", url)...)
	if err == nil {
		return true, nil
	}
	if isNotFound(result, err) {
		return false, nil
	}
	return false, commandError("svn info", result, err)
}

func (s *SVNBackend) writeTemp(hash string, reader io.Reader) (string, error) {
	if s.tempDir != "" {
		if err := os.MkdirAll(s.tempDir, 0o755); err != nil {
			return "", err
		}
	}
	tmp, err := os.CreateTemp(s.tempDir, "vaps-svn-backup-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := utils.NewIoHash()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), reader); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	actual := utils.IoHashDigestHex(hasher)
	if actual != hash {
		return "", fmt.Errorf("%w: got %s want %s", ErrBackupHashMismatch, actual, hash)
	}
	cleanup = false
	return tmpPath, nil
}

func (s *SVNBackend) svnArgs(args ...string) []string {
	result := append([]string{}, args...)
	result = append(result, "--non-interactive")
	return append(result, s.authArgs()...)
}

func (s *SVNBackend) svnmuccArgs(message string, actions ...string) []string {
	result := []string{"--non-interactive", "-m", message}
	result = append(result, s.authArgs()...)
	return append(result, actions...)
}

func (s *SVNBackend) authArgs() []string {
	args := []string{}
	if s.username != "" {
		args = append(args, "--username", s.username)
	}
	if s.password != "" {
		args = append(args, "--password", s.password)
	}
	return args
}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

func parsePayloadListEntry(entry string) (Object, bool) {
	entry = strings.Trim(strings.ReplaceAll(entry, "\\", "/"), "/ \t\r")
	if entry == "" || strings.HasSuffix(entry, "/") {
		return Object{}, false
	}
	entry = strings.TrimPrefix(entry, "blobs/")
	parts := strings.Split(entry, "/")
	if len(parts) != 4 {
		return Object{}, false
	}
	if path.Ext(parts[3]) != ".upayload" {
		return Object{}, false
	}
	hash := parts[0] + parts[1] + parts[2] + strings.TrimSuffix(parts[3], ".upayload")
	if !blobstore.ValidHash(hash) || parts[0] != hash[:2] || parts[1] != hash[2:4] || parts[2] != hash[4:6] {
		return Object{}, false
	}
	rel, err := payloadPath(hash)
	if err != nil {
		return Object{}, false
	}
	return Object{Hash: hash, Path: rel}, true
}

func payloadPath(hash string) (string, error) {
	if !blobstore.ValidHash(hash) {
		return "", blobstore.ErrInvalidHash
	}
	return path.Join(hash[:2], hash[2:4], hash[4:6], hash[6:]+".upayload"), nil
}

func payloadDirs(hash string) []string {
	return []string{hash[:2], path.Join(hash[:2], hash[2:4]), path.Join(hash[:2], hash[2:4], hash[4:6])}
}

func joinURL(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

func commandError(operation string, result CommandResult, err error) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	if message == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, message)
}

func isNotFound(result CommandResult, err error) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + err.Error())
	return strings.Contains(text, "e160013") ||
		strings.Contains(text, "w160013") ||
		strings.Contains(text, "not found") ||
		strings.Contains(text, "non-existent") ||
		strings.Contains(text, "does not exist") ||
		strings.Contains(text, "doesn't exist")
}

func isAlreadyExists(result CommandResult, err error) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "already exists") || strings.Contains(text, "e160020")
}
