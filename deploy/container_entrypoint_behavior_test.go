package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerEntrypointRegisterRequiresReadyAndFixesJournal(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable on this platform")
	}
	temp := t.TempDir()
	nodeBin := writeExecutableFixture(t, temp, "node", "#!/bin/sh\nexit 0\n")
	capture := filepath.Join(temp, "register.args")
	registerBin := writeExecutableFixture(t, temp, "miner-register", `#!/bin/sh
printf '%s\n' "$@" > "$ENTRYPOINT_CAPTURE"
case " $* " in
  *" --private-key-fd=3 "*)
    IFS= read -r ignored <&3
    printf '%s\n' 'fd3=read' >> "$ENTRYPOINT_CAPTURE"
    ;;
esac
`)
	secretValue := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	privateKey := filepath.Join(temp, "private_key")
	if err := os.WriteFile(privateKey, []byte(secretValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join("container-node-entrypoint.sh")
	env := append(os.Environ(),
		"DEX_NODE_BIN="+nodeBin,
		"DEX_MINER_REGISTER_BIN="+registerBin,
		"DEX_PRIVATE_KEY_FILE="+privateKey,
		"ENTRYPOINT_CAPTURE="+capture,
	)
	run := func(args ...string) (string, error) {
		command := exec.Command(sh, append([]string{entrypoint}, args...)...)
		command.Env = env
		output, runErr := command.CombinedOutput()
		return string(output), runErr
	}

	output, err := run("register", "--endpoint=203.0.113.10:6000")
	if err == nil || !strings.Contains(output, "register requires --confirm-ready") {
		t.Fatalf("register without readiness was not rejected: err=%v output=%q", err, output)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Fatalf("register binary ran before readiness: %v", statErr)
	}

	output, err = run("register", "--confirm-ready", "--state-file=/tmp/alternate.json")
	if err == nil || !strings.Contains(output, "journal path is fixed") {
		t.Fatalf("alternate journal was not rejected: err=%v output=%q", err, output)
	}
	if _, statErr := os.Stat(capture); !os.IsNotExist(statErr) {
		t.Fatalf("register binary ran with an alternate journal: %v", statErr)
	}

	output, err = run("register", "--confirm-ready", "--endpoint=203.0.113.10:6000")
	if err != nil {
		t.Fatalf("ready registration failed: %v output=%q", err, output)
	}
	arguments, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(arguments)
	for _, required := range []string{"--private-key-fd=3", "--state-file=/state/registration.json", "fd3=read"} {
		if !strings.Contains(got, required) {
			t.Errorf("register invocation is missing %q: %s", required, got)
		}
	}
	for _, forbidden := range []string{"--confirm-ready", secretValue} {
		if strings.Contains(got, forbidden) || strings.Contains(output, forbidden) {
			t.Errorf("register invocation exposed internal input %q", forbidden)
		}
	}
}

func TestContainerEntrypointEndpointUpdateRequiresReadyAndSeparateJournal(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable on this platform")
	}
	temp := t.TempDir()
	nodeBin := writeExecutableFixture(t, temp, "node", "#!/bin/sh\nexit 0\n")
	capture := filepath.Join(temp, "endpoint-update.args")
	registerBin := writeExecutableFixture(t, temp, "miner-register", `#!/bin/sh
printf '%s\n' "$@" > "$ENTRYPOINT_CAPTURE"
IFS= read -r ignored <&3
printf '%s\n' 'fd3=read' >> "$ENTRYPOINT_CAPTURE"
`)
	privateKey := filepath.Join(temp, "private_key")
	if err := os.WriteFile(privateKey, []byte(strings.Repeat("01", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join("container-node-entrypoint.sh")
	env := append(os.Environ(),
		"DEX_NODE_BIN="+nodeBin,
		"DEX_MINER_REGISTER_BIN="+registerBin,
		"DEX_PRIVATE_KEY_FILE="+privateKey,
		"ENTRYPOINT_CAPTURE="+capture,
	)
	run := func(args ...string) (string, error) {
		command := exec.Command(sh, append([]string{entrypoint}, args...)...)
		command.Env = env
		output, runErr := command.CombinedOutput()
		return string(output), runErr
	}

	if output, err := run("update-endpoint", "--endpoint=192.168.1.7:6000"); err == nil || !strings.Contains(output, "requires --confirm-ready") {
		t.Fatalf("endpoint update without readiness was not rejected: err=%v output=%q", err, output)
	}
	if output, err := run("update-endpoint", "--confirm-ready", "--state-file=/tmp/alternate.json"); err == nil || !strings.Contains(output, "journal path is fixed") {
		t.Fatalf("alternate endpoint journal was not rejected: err=%v output=%q", err, output)
	}
	output, err := run("update-endpoint", "--confirm-ready", "--expected-current-endpoint=192.168.1.10:6000", "--endpoint=192.168.1.7:6000")
	if err != nil {
		t.Fatalf("ready endpoint update failed: %v output=%q", err, output)
	}
	arguments, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	got := string(arguments)
	for _, required := range []string{"--private-key-fd=3", "--update-endpoint", "--state-file=/state/endpoint-update.json", "--expected-current-endpoint=192.168.1.10:6000", "--endpoint=192.168.1.7:6000", "fd3=read"} {
		if !strings.Contains(got, required) {
			t.Errorf("endpoint invocation is missing %q: %s", required, got)
		}
	}
	if strings.Contains(got, "--confirm-ready") {
		t.Fatal("confirmation marker leaked into endpoint helper arguments")
	}
}

func writeExecutableFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
