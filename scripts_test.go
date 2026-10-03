package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScriptsEmbedded(t *testing.T) {
	err := fs.WalkDir(scriptFS, "scripts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := scriptFS.ReadFile(path)
		if strings.Contains(string(b), "\r") {
			t.Errorf("%s has CRLF line ends, sh would choke on them", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for kind, s := range taskScripts {
		if strings.TrimSpace(s) == "" {
			t.Errorf("task %s is empty", kind)
		}
	}
}

// The tests below run the real scripts with fake git and docker that log
// their arguments, one [argument] per word, so spaces stay visible.

const fakeGitScript = `#!/bin/sh
echo "git $*" >> "$LOG"
while [ "$1" = "-c" ]; do shift 2; done
case "$1" in
config) exit 1 ;;
rev-parse) echo oldsha ;;
pull) [ -z "$FAIL_PULL" ] ;;
log) echo "abc1234 Новая версия" ;;
esac
`

const fakeDockerScript = `#!/bin/sh
{ printf 'docker'; for a in "$@"; do printf ' [%s]' "$a"; done; echo; } >> "$LOG"
case "$1" in
compose) for a in "$@"; do [ "$a" = ps ] && echo cid1; done ;;
inspect) case "$3" in *.Image*) echo sha256:oldimage ;; *) echo "$INSPECT" ;; esac ;;
run) [ -z "$FAIL_RUN" ] || exit 125 ;;
esac
exit 0
`

func runScript(t *testing.T, script string, env []string, args ...string) (out, log string, ok bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"git": fakeGitScript, "docker": fakeDockerScript} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	logFile := filepath.Join(t.TempDir(), "log")
	cmd := exec.Command(sh, append([]string{"-c", "set -e\n" + script, "prichal"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "LOG="+logFile, "PRICHAL_SETTLE=0")
	cmd.Env = append(cmd.Env, env...)
	b, err := cmd.CombinedOutput()
	l, _ := os.ReadFile(logFile)
	return string(b), string(l), err == nil
}

func TestGitUpdateScript(t *testing.T) {
	wd := t.TempDir()
	files := filepath.Join(wd, "compose.yaml") + "," + filepath.Join(wd, "my dir", "override.yaml")

	out, log, ok := runScript(t, gitUpdateScript, []string{"INSPECT=true false none"}, "proj", "web", wd, files)
	if !ok || !strings.Contains(out, "== Готово") {
		t.Fatalf("update failed:\n%s\n%s", out, log)
	}
	// Compose files reach docker as whole arguments, spaces included.
	want := "docker [compose] [-p] [proj] [-f] [" + filepath.Join(wd, "compose.yaml") + "] [-f] [" + filepath.Join(wd, "my dir", "override.yaml") + "] [build] [web]"
	if !strings.Contains(log, want) {
		t.Errorf("want %q in\n%s", want, log)
	}
	if strings.Contains(log, "reset") {
		t.Errorf("no rollback on success:\n%s", log)
	}

	// The new version does not come up: back to the old commit.
	out, log, ok = runScript(t, gitUpdateScript, []string{"INSPECT=false false none"}, "proj", "web", wd, files)
	// --keep: files edited on the server survive the rollback (--hard deleted them).
	if ok || !strings.Contains(out, "Возвращаю прежнюю версию") || !strings.Contains(log, "reset --keep oldsha") || strings.Contains(log, "--hard") ||
		!strings.Contains(log, "[up] [-d] [--build] [web]") {
		t.Errorf("rollback expected:\n%s\n%s", out, log)
	}

	// Pull fails: nothing to roll back.
	_, log, ok = runScript(t, gitUpdateScript, []string{"FAIL_PULL=1"}, "proj", "web", wd, files)
	if ok || strings.Contains(log, "reset") || strings.Contains(log, "[build]") {
		t.Errorf("failed pull must stop before building:\n%s", log)
	}
}

// "Running" is not "ready": a container with a healthcheck must turn healthy,
// one that stays unhealthy or starting past the deadline fails the update.
func TestUpdateScriptsWaitForHealth(t *testing.T) {
	wd := t.TempDir()
	files := filepath.Join(wd, "compose.yaml")
	for _, c := range []struct {
		inspect string
		wait    string
		ok      bool
	}{
		{"true false healthy", "0", true},
		{"true false starting", "0", false},
		{"true false unhealthy", "60", false},
		{"true true none", "60", false}, // restart loop
	} {
		out, log, ok := runScript(t, gitUpdateScript, []string{"INSPECT=" + c.inspect, "PRICHAL_WAIT=" + c.wait}, "proj", "web", wd, files)
		if ok != c.ok {
			t.Errorf("git update with %q: ok=%v, want %v\n%s\n%s", c.inspect, ok, c.ok, out, log)
		}
		out, log, ok = runScript(t, appUpdateScript, []string{"INSPECT=" + c.inspect, "PRICHAL_WAIT=" + c.wait, "PRICHAL_BACKUPS=" + filepath.Join(t.TempDir(), "b")},
			"proj", "web", wd, files, "0", helperImage)
		if ok != c.ok || ok == strings.Contains(out, "Новая версия не заработала") {
			t.Errorf("app update with %q: ok=%v, want %v\n%s\n%s", c.inspect, ok, c.ok, out, log)
		}
	}
}

// An app update that "succeeds" with a crashed container must fail, keep the
// crashed container for its log and name the image it ran before.
func TestAppUpdateScriptNotReady(t *testing.T) {
	wd := t.TempDir()
	out, log, ok := runScript(t, appUpdateScript, []string{"INSPECT=false false none", "PRICHAL_BACKUPS=" + filepath.Join(t.TempDir(), "b")},
		"proj", "web", wd, filepath.Join(wd, "compose.yaml"), "0", helperImage)
	if ok || !strings.Contains(out, "Новая версия не заработала") || !strings.Contains(out, "sha256:oldimage") {
		t.Fatalf("failure with the old image named expected:\n%s\n%s", out, log)
	}
	if strings.Contains(log, "[start]") || strings.Contains(log, "prune") {
		t.Errorf("nothing may be started or pruned after a failed update:\n%s", log)
	}
}

func TestAppUpdateScriptBackup(t *testing.T) {
	wd := t.TempDir()
	compose := filepath.Join(wd, "my compose.yaml")
	if err := os.WriteFile(compose, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(t.TempDir(), "backups")
	out, log, ok := runScript(t, appUpdateScript, []string{"PRICHAL_BACKUPS=" + backups, "INSPECT=true false none"},
		"proj", "web", wd, compose, "1", helperImage, "proj_data")
	if !ok || !strings.Contains(out, "== Готово") {
		t.Fatalf("update failed:\n%s\n%s", out, log)
	}
	for _, want := range []string{
		"[compose] [-p] [proj] [-f] [" + compose + "] [pull] [web]",
		"[compose] [-p] [proj] [-f] [" + compose + "] [stop] [web]",
		"[-v] [proj_data:/data:ro]",
		"[" + helperImage + "] [tar]",
		"[up] [-d] [web]",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("want %q in\n%s", want, log)
		}
	}
	st, err := os.Stat(backups)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("backups must be root-only, got %v", st.Mode().Perm())
	}
	copies, _ := filepath.Glob(filepath.Join(backups, "proj", "*", "my compose.yaml"))
	if len(copies) != 1 {
		t.Errorf("compose file not copied next to the data: %v", copies)
	}
}

func TestTaskWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	dir := t.TempDir()
	run := func(id, script string, args ...string) (string, string) {
		cmd := exec.Command("sh", append([]string{"-c", taskWrapper(dir, id, script), "prichal"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		log, _ := os.ReadFile(filepath.Join(dir, id+".log"))
		exit, _ := os.ReadFile(filepath.Join(dir, id+".exit"))
		return string(log), strings.TrimSpace(string(exit))
	}
	if log, exit := run("ok-1", `echo "args: $*"`, "a", "b c"); exit != "0" || log != "args: a b c\n" {
		t.Errorf("ok: exit %q log %q", exit, log)
	}
	// The exit file is renamed into place: nothing half-written stays behind.
	if tmp, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(tmp) != 0 {
		t.Errorf("leftover temp files: %v", tmp)
	}
	// set -e: the first failing command ends the task with its code.
	if log, exit := run("bad-1", "echo one\nfalse\necho two"); exit == "0" || strings.Contains(log, "two") {
		t.Errorf("bad: exit %q log %q", exit, log)
	}
}

// A failed backup must bring the old container back, not recreate it from
// the freshly pulled image (that would be an update without a backup).
func TestAppUpdateScriptBackupFails(t *testing.T) {
	wd := t.TempDir()
	out, log, ok := runScript(t, appUpdateScript, []string{"PRICHAL_BACKUPS=" + filepath.Join(t.TempDir(), "b"), "FAIL_RUN=1"},
		"proj", "web", wd, filepath.Join(wd, "compose.yaml"), "1", helperImage, "proj_data")
	if ok || !strings.Contains(out, "Запускаю web обратно") {
		t.Fatalf("failure expected:\n%s\n%s", out, log)
	}
	if !strings.Contains(log, "[start] [web]") || strings.Contains(log, "[up]") {
		t.Errorf("the old container must start, nothing recreated:\n%s", log)
	}
}

// systemd replaces "$$" with "$" and expands "${X}" in the command line;
// doubling every "$" gives sh exactly what the task script says.
func TestSystemdRunEscapes(t *testing.T) {
	wrapper := taskWrapper(taskDir, "app-1", appUpdateScript)
	cmd := systemdRunCmd("app-1", "обновление n8n", wrapper, []string{"proj", "/srv/$weird dir"})
	unescape := func(s string) string { return strings.ReplaceAll(s, "$$", "$") }
	if got := unescape(cmd[6]); got != wrapper {
		t.Errorf("wrapper changed on the way through systemd")
	}
	if !strings.Contains(cmd[6], `prichal-host-backup-$$$$`) {
		t.Errorf("$$ must reach systemd as $$$$")
	}
	if cmd[5] != "-c" || cmd[7] != "prichal" || unescape(cmd[9]) != "/srv/$weird dir" {
		t.Errorf("command: %q", cmd)
	}
}
