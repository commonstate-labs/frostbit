package updater

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
)

const processFixtureEnv = "DEX_UPDATER_PROCESS_FIXTURE"

type bootstrapProcessFixture struct {
	Config    Config
	Stable    Prepared
	Candidate Prepared
	Fault     string
	DataDir   string
	Trace     string
}

type bootstrapProcessEvent struct {
	Path string
	PID  int
	Step string
}

                                                                          
                                                                              
                                                                           
func TestProcessBootstrapRollbackPreservesDataAndQuarantinesCandidate(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		t.Skip("production automatic exec requires an unprivileged Linux node")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"unconfirmed-exit", "crash-with-open-database"} {
		t.Run(fault, func(t *testing.T) {
			f := newEmbeddedFixture(t)
			root := filepath.Dir(f.cfg.Executable)
			f.cfg.Executable = filepath.Join(root, "baseline.exe")
			f.cfg.Info.GOARCH, f.manifest.GOARCH = runtime.GOARCH, runtime.GOARCH
			if err := os.WriteFile(f.cfg.Executable, binary, 0700); err != nil {
				t.Fatal(err)
			}
			f.agent, err = New(f.cfg, f.dep)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.agent.setupCache(); err != nil {
				t.Fatal(err)
			}
			makePrepared := func(version, commit, role string, releaseID int64) Prepared {
				t.Helper()
				body := append(append([]byte(nil), binary...), []byte("\nDEX_UPDATER_PROCESS_ROLE="+role+"\n")...)
				digest := HashBytes(body)
				path := filepath.Join(f.cfg.StateDir, "versions", digest, "node")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, body, 0700); err != nil {
					t.Fatal(err)
				}
				manifest := f.manifest
				manifest.Tag, manifest.Version, manifest.Commit = version, version, commit
				manifest.Asset = Asset{Name: "dex-node-" + version + "-linux-" + runtime.GOARCH, SHA256: digest, Size: int64(len(body))}
				return Prepared{Path: path, ReleaseID: releaseID, Tag: version, Version: version, Commit: commit, Digest: digest, Manifest: manifest}
			}
			stable := makePrepared("v1.1.0", strings.Repeat("b", 40), "stable", 41)
			candidate := makePrepared("v1.2.0", strings.Repeat("c", 40), "candidate", 42)
			if err := f.agent.saveState(diskState{HighReleaseID: stable.ReleaseID, HighVersion: stable.Version, Stable: &stable}); err != nil {
				t.Fatal(err)
			}
			if err := Activate(context.Background(), f.cfg, f.dep, candidate); err != nil {
				t.Fatal(err)
			}
			fixture := bootstrapProcessFixture{Config: f.cfg, Stable: stable, Candidate: candidate, Fault: fault, DataDir: filepath.Join(root, "node-data"), Trace: filepath.Join(root, "trace.jsonl")}
			database, err := pebble.Open(fixture.DataDir, &pebble.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Set([]byte("finalized-state"), []byte("height=123;root=unchanged"), pebble.Sync); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			fixturePath := filepath.Join(root, "process-fixture.json")
			if err := atomicJSON(fixturePath, fixture); err != nil {
				t.Fatal(err)
			}
			run := func(wantExit int) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, f.cfg.Executable, "-test.run=^TestProcessBootstrapHelper$", "-test.count=1")
				command.WaitDelay = time.Second
				command.Env = append(os.Environ(), processFixtureEnv+"="+fixturePath)
				output, runErr := command.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("bootstrap subprocess timeout: %v\n%s", ctx.Err(), output)
				}
				exitCode := 0
				if runErr != nil {
					var exitError *exec.ExitError
					if !errors.As(runErr, &exitError) {
						t.Fatalf("start bootstrap subprocess: %v\n%s", runErr, output)
					}
					exitCode = exitError.ExitCode()
				}
				if exitCode != wantExit {
					t.Fatalf("bootstrap exit=%d want=%d\n%s", exitCode, wantExit, output)
				}
			}
			wantExit := 47
			if fault == "crash-with-open-database" {
				wantExit = 48
			}
			run(wantExit)
			state := f.state(t)
			if state.Pending == nil || state.Pending.Attempt != 1 || state.Pending.Prepared.Path != candidate.Path || state.Stable == nil || state.Stable.Path != stable.Path {
				t.Fatalf("failed candidate incorrectly confirmed: %+v", state)
			}
			                                                                       
			                                                                       
			run(0)
			run(0)
			state = f.state(t)
			if state.Pending != nil || state.FailedDigest != candidate.Digest || state.HighReleaseID != candidate.ReleaseID || state.HighVersion != candidate.Version || state.Stable == nil || state.Stable.Path != stable.Path {
				t.Fatalf("rollback lost selection or high-water mark: %+v", state)
			}
			trace, err := os.Open(fixture.Trace)
			if err != nil {
				t.Fatal(err)
			}
			defer trace.Close()
			var events []bootstrapProcessEvent
			scanner := bufio.NewScanner(trace)
			for scanner.Scan() {
				var event bootstrapProcessEvent
				if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
					t.Fatal(err)
				}
				events = append(events, event)
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			wantPaths := []string{f.cfg.Executable, candidate.Path, f.cfg.Executable, stable.Path, f.cfg.Executable, stable.Path}
			if len(events) != len(wantPaths) {
				t.Fatalf("unexpected executable history: %+v", events)
			}
			for i, event := range events {
				if event.Path != wantPaths[i] || event.Step != "bootstrap" {
					t.Fatalf("executable history[%d]=%+v want=%s", i, event, wantPaths[i])
				}
				if runtime.GOOS == "linux" && i%2 == 1 && event.PID != events[i-1].PID {
					t.Fatalf("exec changed PID: %+v -> %+v", events[i-1], event)
				}
			}
			database, err = pebble.Open(fixture.DataDir, &pebble.Options{})
			if err != nil {
				t.Fatalf("original database cannot reopen after rollback: %v", err)
			}
			defer database.Close()
			assertProcessData(t, database, "finalized-state", "height=123;root=unchanged")
			if fault == "crash-with-open-database" {
				assertProcessData(t, database, "candidate-durable-write", "survives-crash")
			}
			t.Logf("3 bounded supervisor launches, 6 executable images, candidate quarantined once; %s database reopened", fault)
		})
	}
}

func assertProcessData(t *testing.T, database *pebble.DB, key, want string) {
	t.Helper()
	value, closer, err := database.Get([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if string(value) != want {
		t.Fatalf("database %s=%q want=%q", key, value, want)
	}
}

                                                                             
                                                                              
func TestProcessBootstrapHelper(t *testing.T) {
	fixturePath := os.Getenv(processFixtureEnv)
	if fixturePath == "" {
		return
	}
	bytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture bootstrapProcessFixture
	if err := json.Unmarshal(bytes, &fixture); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture.Config.Executable = executable
	for _, selected := range []Prepared{fixture.Stable, fixture.Candidate} {
		if executable == selected.Path {
			fixture.Config.Info.Version, fixture.Config.Info.Commit = selected.Version, selected.Commit
		}
	}
	trace, err := os.OpenFile(fixture.Trace, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(trace).Encode(bootstrapProcessEvent{Path: executable, PID: os.Getpid(), Step: "bootstrap"}); err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	dependencies := bootstrapProcessDependencies()
	if err := Bootstrap(context.Background(), fixture.Config, dependencies); err != nil {
		t.Fatal(err)
	}
	pending, err := PendingConfirmation(context.Background(), fixture.Config, dependencies)
	if err != nil || pending != (executable == fixture.Candidate.Path) {
		t.Fatalf("pending identity: pending=%t executable=%s err=%v", pending, executable, err)
	}
	if executable != fixture.Stable.Path && executable != fixture.Candidate.Path {
		t.Fatalf("baseline continued without selecting a cached image: %s", executable)
	}
	database, err := pebble.Open(fixture.DataDir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertProcessData(t, database, "finalized-state", "height=123;root=unchanged")
	if executable == fixture.Candidate.Path && fixture.Fault == "crash-with-open-database" {
		if err := database.Set([]byte("candidate-durable-write"), []byte("survives-crash"), pebble.Sync); err != nil {
			t.Fatal(err)
		}
		os.Exit(48)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if executable == fixture.Candidate.Path {
		os.Exit(47)
	}
	fmt.Println("confirmed stable image reopened original database")
}
