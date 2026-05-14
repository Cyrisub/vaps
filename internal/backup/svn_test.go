package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

const helloHash = "ea8f163db38682925e4491c5e58d4bb3506ef8c1"

func TestSVNListParsesPayloadObjects(t *testing.T) {
	runner := &fakeRunner{
		run: func(name string, args ...string) (CommandResult, error) {
			if name != "svn" || !hasArgs(args, "list", "-R", "https://svn.example/repo") {
				t.Fatalf("unexpected command %s %v", name, args)
			}

			return CommandResult{Stdout: strings.Join([]string{
				"ea/",
				"ea/8f/",
				"ea/8f/16/",
				"ea/8f/16/3db38682925e4491c5e58d4bb3506ef8c1.upayload",
				"ea/8f/16/not-a-payload.txt",
				"bb/8f/16/not-a-matching-shard.upayload",
			}, "\n")}, nil
		},
	}
	backend, err := NewSVNBackend(SVNConfig{URL: "https://svn.example/repo/", Runner: runner})
	if err != nil {
		t.Fatalf("NewSVNBackend returned error: %v", err)
	}

	objects, err := backend.List(context.Background())
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("len(objects) = %d, want 1: %#v", len(objects), objects)
	}
	if objects[0].Hash != helloHash {
		t.Fatalf("Hash = %q, want %q", objects[0].Hash, helloHash)
	}
	if objects[0].Path != "ea/8f/16/3db38682925e4491c5e58d4bb3506ef8c1.upayload" {
		t.Fatalf("Path = %q", objects[0].Path)
	}
}

func TestSVNExistsMapsNotFoundToFalse(t *testing.T) {
	runner := &fakeRunner{
		run: func(name string, args ...string) (CommandResult, error) {
			return CommandResult{Stderr: "svn: E160013: path not found"}, errors.New("exit status 1")
		},
	}
	backend, err := NewSVNBackend(SVNConfig{URL: "https://svn.example/repo", Runner: runner})
	if err != nil {
		t.Fatalf("NewSVNBackend returned error: %v", err)
	}

	exists, err := backend.Exists(context.Background(), helloHash)
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if exists {
		t.Fatalf("Exists = true, want false")
	}
}

func TestSVNOpenDownloadsPayloadWithSVNCat(t *testing.T) {
	runner := &fakeRunner{
		run: func(name string, args ...string) (CommandResult, error) {
			if name != "svn" || !hasArgs(args, "cat", "https://svn.example/repo/ea/8f/16/3db38682925e4491c5e58d4bb3506ef8c1.upayload") {
				t.Fatalf("unexpected command %s %v", name, args)
			}
			return CommandResult{Stdout: "hello"}, nil
		},
	}
	backend, err := NewSVNBackend(SVNConfig{URL: "https://svn.example/repo", Runner: runner})
	if err != nil {
		t.Fatalf("NewSVNBackend returned error: %v", err)
	}
	reader, err := backend.Open(context.Background(), helloHash)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer reader.Close()
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload = %q, want hello", string(payload))
	}
}

func TestSVNPutCreatesDirectoriesAndUploadsWithSVNMucc(t *testing.T) {
	runner := &fakeRunner{
		run: func(name string, args ...string) (CommandResult, error) {
			switch name {
			case "svn":
				if !hasArg(args, "info") {
					t.Fatalf("unexpected svn args: %v", args)
				}
				return CommandResult{Stderr: "svn: E160013: path not found"}, errors.New("exit status 1")
			case "svnmucc":
				if hasArg(args, "put") {
					putIndex := indexArg(args, "put")
					if putIndex < 0 || putIndex+2 >= len(args) {
						t.Fatalf("invalid svnmucc put args: %v", args)
					}
					payload, err := os.ReadFile(args[putIndex+1])
					if err != nil {
						t.Fatalf("read temp payload: %v", err)
					}
					if string(payload) != "hello" {
						t.Fatalf("uploaded payload = %q, want hello", string(payload))
					}
					wantURL := "https://svn.example/repo/ea/8f/16/3db38682925e4491c5e58d4bb3506ef8c1.upayload"
					if args[putIndex+2] != wantURL {
						t.Fatalf("put target = %q, want %q", args[putIndex+2], wantURL)
					}
					return CommandResult{}, nil
				}
				if !hasArg(args, "mkdir") {
					t.Fatalf("unexpected svnmucc args: %v", args)
				}
				return CommandResult{}, nil
			default:
				t.Fatalf("unexpected command: %s", name)
			}
			return CommandResult{}, nil
		},
	}
	backend, err := NewSVNBackend(SVNConfig{URL: "https://svn.example/repo", TempDir: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatalf("NewSVNBackend returned error: %v", err)
	}

	object, err := backend.Put(context.Background(), helloHash, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if object.Hash != helloHash || object.Path != "ea/8f/16/3db38682925e4491c5e58d4bb3506ef8c1.upayload" {
		t.Fatalf("object = %#v", object)
	}
	if runner.count("svnmucc", "mkdir") != 1 {
		t.Fatalf("svnmucc mkdir command count = %d, want 1; calls=%#v", runner.count("svnmucc", "mkdir"), runner.calls)
	}
	if runner.count("svnmucc", "put") != 1 {
		t.Fatalf("svnmucc put count = %d, want 1; calls=%#v", runner.count("svnmucc", "put"), runner.calls)
	}
}

func TestSVNPutRejectsHashMismatchBeforeRunningCommands(t *testing.T) {
	runner := &fakeRunner{}
	backend, err := NewSVNBackend(SVNConfig{URL: "https://svn.example/repo", TempDir: t.TempDir(), Runner: runner})
	if err != nil {
		t.Fatalf("NewSVNBackend returned error: %v", err)
	}

	_, err = backend.Put(context.Background(), helloHash, strings.NewReader("goodbye"))
	if !errors.Is(err, ErrBackupHashMismatch) {
		t.Fatalf("Put error = %v, want ErrBackupHashMismatch", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands were run despite hash mismatch: %#v", runner.calls)
	}
}

type fakeRunner struct {
	calls []fakeCall
	run   func(name string, args ...string) (CommandResult, error)
}

type fakeCall struct {
	name string
	args []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	r.calls = append(r.calls, fakeCall{name: name, args: append([]string{}, args...)})
	if r.run == nil {
		return CommandResult{}, nil
	}
	return r.run(name, args...)
}

func (r *fakeRunner) count(name, arg string) int {
	count := 0
	for _, call := range r.calls {
		if call.name == name && hasArg(call.args, arg) {
			count++
		}
	}
	return count
}

func hasArgs(args []string, want ...string) bool {
	for _, item := range want {
		if !hasArg(args, item) {
			return false
		}
	}
	return true
}

func hasArg(args []string, want string) bool {
	return indexArg(args, want) >= 0
}

func indexArg(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}
