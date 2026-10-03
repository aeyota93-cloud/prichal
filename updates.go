package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Updates: system packages (apt, dnf/yum, apk), docker compose apps, reboot.
//
// Long jobs run on the host, not in the panel's container: as transient
// systemd units (systemd-run) where systemd exists, otherwise in a detached
// helper container. With systemd they survive the panel being restarted, e.g.
// when the docker package itself is upgraded. Each job writes its output to
// /var/lib/prichal/tasks/<id>.log and its exit code to <id>.exit.

const (
	taskDir      = "/var/lib/prichal/tasks"
	listCacheFor = 60 * time.Second
)

type PkgUpdate struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Group    string `json:"group"` // docker, kernel, security, other
	Security bool   `json:"security"`
	Summary  string `json:"summary"`
}

type SystemView struct {
	PM             string      `json:"pm"` // apt, dnf, yum, apk, pacman, zypper, none
	Supported      bool        `json:"supported"`
	Init           string      `json:"init"` // systemd, other
	OS             string      `json:"os"`
	Kernel         string      `json:"kernel"`
	Packages       []PkgUpdate `json:"packages"`
	ListsAt        int64       `json:"listsAt"`
	RebootRequired bool        `json:"rebootRequired"`
	RebootPkgs     []string    `json:"rebootPkgs"`
}

type Task struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"` // check, install, app, git
	Title    string   `json:"title"`
	Packages []string `json:"packages,omitempty"`
	Started  int64    `json:"started"`
	Finished int64    `json:"finished,omitempty"`
	Exit     *int     `json:"exit,omitempty"`
	State    string   `json:"state"` // running, done, failed, interrupted
	Runner   string   `json:"runner,omitempty"`
}

type UpdatesView struct {
	System *SystemView `json:"system"`
	Apps   *AppsView   `json:"apps"`
	Task   *Task       `json:"task"`
	Error  string      `json:"error,omitempty"`
	AppErr string      `json:"appsError,omitempty"`
}

type Updates struct {
	docker   *Docker
	apps     *Apps
	hostRoot string
	state    string   // path of updates.json in DATA_DIR ("" = memory only)
	journal  *Journal // finished tasks go to the timeline

	mu      sync.Mutex
	list    *SystemView
	listAt  time.Time
	task    *Task
	listMu  sync.Mutex // one host listing at a time
	startMu sync.Mutex // one task (or reboot) start at a time
	unitAt  time.Time  // when a task's systemd unit was last looked at
}

func NewUpdates(d *Docker, apps *Apps, hostRoot, dataDir string) *Updates {
	u := &Updates{docker: d, apps: apps, hostRoot: hostRoot}
	if dataDir != "" {
		u.state = filepath.Join(dataDir, "updates.json")
		if b, err := os.ReadFile(u.state); err == nil {
			var s struct{ Task *Task }
			if json.Unmarshal(b, &s) == nil {
				u.task = s.Task
			}
		}
	}
	return u
}

func (u *Updates) saveLocked() {
	if u.state == "" {
		return
	}
	_ = writeJSONAtomic(u.state, map[string]any{"task": u.task})
}

// hostFile reads a file of the host: through the read-only /host mount in
// production, or with a helper container when running outside (development).
func (u *Updates) hostFile(ctx context.Context, path string) ([]byte, error) {
	if u.hostRoot != "" && u.hostRoot != "/" {
		return os.ReadFile(filepath.Join(u.hostRoot, path))
	}
	out, code, err := u.docker.HostRun(ctx, "cat", path)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, os.ErrNotExist
	}
	return []byte(out), nil
}

// ---------- Listing ----------

var (
	aptLine = regexp.MustCompile(`^([^/\s]+)/(\S+)\s+(\S+)\s+\S+\s+\[upgradable from: ([^\]]+)\]`)
	dnfLine = regexp.MustCompile(`^(\S+\.\S+)\s+(\S+)\s+(\S+)\s*$`)
	apkLine = regexp.MustCompile(`^(\S+)-(\S+-r\d+)\s+<\s+(\S+)`)
)

var dockerPkgs = map[string]bool{
	"docker-ce": true, "docker-ce-cli": true, "containerd.io": true, "docker-buildx-plugin": true,
	"docker-compose-plugin": true, "docker-ce-rootless-extras": true, "docker-model-plugin": true,
	"docker.io": true, "docker": true, "containerd": true, "docker-engine": true, "docker-cli": true,
	"docker-compose": true, "runc": true,
}

// isKernelPkg: the kernel itself and its meta packages. linux-libc-dev and
// linux-firmware are ordinary packages that need no reboot.
func isKernelPkg(name string) bool {
	base, _, _ := strings.Cut(name, ".") // dnf names carry .arch
	for _, p := range []string{"linux-image", "linux-headers", "linux-modules", "linux-generic", "linux-virtual",
		"linux-kvm", "linux-tools", "linux-lts", "linux-virt", "kernel"} {
		if strings.HasPrefix(base, p) && !strings.HasPrefix(base, "kernel-headers") && !strings.HasPrefix(base, "kernel-tools") {
			return true
		}
	}
	return false
}

func pkgGroup(name string, security bool) string {
	base, _, _ := strings.Cut(name, ".")
	switch {
	case dockerPkgs[name] || dockerPkgs[base]:
		return "docker"
	case isKernelPkg(name):
		return "kernel"
	case security:
		return "security"
	}
	return "other"
}

var supportedPM = map[string]bool{"apt": true, "dnf": true, "yum": true, "apk": true}

func parseListing(out string) *SystemView {
	v := &SystemView{Packages: []PkgUpdate{}, RebootPkgs: []string{}}
	summaries := map[string]string{}
	installed := map[string]string{}
	var security []string
	section := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "@@") {
			key, val, _ := strings.Cut(line[2:], " ")
			switch key {
			case "PM":
				v.PM = val
			case "INIT":
				v.Init = val
			case "OS":
				v.OS = val
			case "KERNEL":
				v.Kernel = val
			case "STAMP":
				v.ListsAt, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			case "REBOOT":
				v.RebootRequired = true
			}
			section = key
			continue
		}
		if line == "" {
			continue
		}
		switch section {
		case "REBOOT":
			v.RebootPkgs = append(v.RebootPkgs, line)
		case "UPGRADABLE":
			switch v.PM {
			case "apt":
				if m := aptLine.FindStringSubmatch(line); m != nil {
					sec := strings.Contains(m[2], "-security")
					v.Packages = append(v.Packages, PkgUpdate{Name: m[1], To: m[3], From: m[4], Security: sec})
				}
			case "dnf", "yum":
				if strings.HasPrefix(line, "Obsoleting") {
					section = "" // what follows is not a list of upgrades
					continue
				}
				if m := dnfLine.FindStringSubmatch(line); m != nil {
					v.Packages = append(v.Packages, PkgUpdate{Name: m[1], To: m[2]})
				}
			case "apk":
				if m := apkLine.FindStringSubmatch(line); m != nil {
					v.Packages = append(v.Packages, PkgUpdate{Name: m[1], From: m[2], To: m[3]})
				}
			}
		case "SUMMARY":
			if name, sum, ok := strings.Cut(line, "\t"); ok {
				summaries[name] = sum
			}
		case "SECURITY":
			if f := strings.Fields(line); len(f) >= 3 {
				security = append(security, f[len(f)-1])
			}
		case "INSTALLED":
			if f := strings.SplitN(line, "\t", 3); len(f) == 3 {
				installed[f[0]] = f[1]
				summaries[f[0]] = f[2]
			}
		}
	}
	v.Supported = supportedPM[v.PM]
	for i := range v.Packages {
		p := &v.Packages[i]
		p.Summary = summaries[p.Name]
		if p.From == "" {
			p.From = installed[p.Name]
		}
		if !p.Security && len(security) > 0 { // dnf: name.arch vs NEVRA of advisories
			base, arch, _ := strings.Cut(p.Name, ".")
			for _, nevra := range security {
				if strings.HasPrefix(nevra, base+"-") && strings.HasSuffix(nevra, "."+arch) {
					p.Security = true
					break
				}
			}
		}
		p.Group = pkgGroup(p.Name, p.Security)
	}
	sort.SliceStable(v.Packages, func(a, b int) bool { return v.Packages[a].Name < v.Packages[b].Name })
	return v
}

func (u *Updates) System(ctx context.Context, fresh bool) (*SystemView, error) {
	u.listMu.Lock()
	defer u.listMu.Unlock()
	u.mu.Lock()
	if !fresh && u.list != nil && time.Since(u.listAt) < listCacheFor {
		v := u.list
		u.mu.Unlock()
		return v, nil
	}
	u.mu.Unlock()
	out, code, err := u.docker.HostRun(ctx, "sh", "-c", listScript)
	if err != nil {
		return nil, err
	}
	if code != 0 && !strings.Contains(out, "@@PM") {
		return nil, fmt.Errorf("не удалось получить список обновлений (код %d)", code)
	}
	v := parseListing(out)
	u.mu.Lock()
	u.list, u.listAt = v, time.Now()
	u.mu.Unlock()
	return v, nil
}

func (u *Updates) invalidate() {
	u.mu.Lock()
	u.list = nil
	u.mu.Unlock()
}

// View is everything the Updates tab shows.
func (u *Updates) View(ctx context.Context, fresh bool) *UpdatesView {
	out := &UpdatesView{}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if a, err := u.apps.View(ctx, fresh); err != nil {
			out.AppErr = err.Error()
		} else {
			out.Apps = a
		}
	}()
	s, err := u.System(ctx, fresh)
	if err != nil {
		out.Error = err.Error()
	} else {
		out.System = s
	}
	wg.Wait()
	out.Task = u.Task(ctx)
	return out
}

// ---------- Tasks ----------

var errBusy = errors.New("уже выполняется другая задача, дождитесь её окончания")

// taskWrapper runs script in a subshell with set -e, writes its output to
// dir/<id>.log and the exit code to dir/<id>.exit, and keeps the last 20 logs.
// The exit file appears whole (written aside, then renamed): a reader never
// sees it empty and mistakes that for success.
func taskWrapper(dir, id, script string) string {
	return fmt.Sprintf(`export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a
mkdir -p %[1]s
( set -e
%[2]s
) > %[1]s/%[3]s.log 2>&1
rc=$?
echo $rc > %[1]s/%[3]s.exit.tmp
mv %[1]s/%[3]s.exit.tmp %[1]s/%[3]s.exit
ls -1t %[1]s/*.log | tail -n +21 | sed 's/\.log$//' | while read -r f; do rm -f "$f.log" "$f.exit" "$f.exit.tmp"; done`,
		dir, script, id)
}

// systemdRunCmd starts the wrapper as a transient unit. systemd treats "$"
// in the command line as its own variables ("$$" became "$", "${X}" would be
// replaced), so every "$" is doubled to reach sh unchanged.
func systemdRunCmd(id, title, wrapper string, args []string) []string {
	esc := func(s string) string { return strings.ReplaceAll(s, "$", "$$") }
	cmd := []string{"systemd-run", "--unit=prichal-" + id, "--description=Причал: " + title, "--", "/bin/sh", "-c", esc(wrapper), "prichal"}
	for _, a := range args {
		cmd = append(cmd, esc(a))
	}
	return cmd
}

// Start runs a task on the host. Only one task runs at a time: the check
// and the start happen under startMu, so two quick clicks cannot both pass.
func (u *Updates) Start(ctx context.Context, kind, title string, args []string) (*Task, error) {
	script, ok := taskScripts[kind]
	if !ok {
		return nil, fmt.Errorf("unknown task %q", kind)
	}
	u.startMu.Lock()
	defer u.startMu.Unlock()
	if t := u.Task(ctx); t != nil && t.State == "running" {
		return nil, errBusy
	}
	sys, err := u.System(ctx, false)
	if err != nil {
		return nil, err
	}
	if err := u.docker.ensureImage(ctx, helperImage); err != nil {
		return nil, err
	}
	now := time.Now()
	id := kind + "-" + now.UTC().Format("20060102-150405")
	wrapper := taskWrapper(taskDir, id, script)
	var cmd []string
	runner := "systemd"
	if sys.Init == "systemd" {
		out, code, err := u.docker.HostRun(ctx, systemdRunCmd(id, title, wrapper, args)...)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, fmt.Errorf("сервер не запустил задачу: %s", strings.TrimSpace(out))
		}
	} else {
		// No systemd: the helper container itself runs the job and stays until
		// it ends. Upgrading Docker from here would interrupt it.
		runner = "container"
		cmd = append([]string{"/bin/sh", "-c", wrapper, "prichal"}, args...)
		if err := u.docker.HostStart(ctx, helperPrefix+"task-"+id, cmd...); err != nil {
			return nil, err
		}
	}
	t := &Task{ID: id, Kind: kind, Title: title, Packages: args, Started: now.Unix(), State: "running", Runner: runner}
	u.mu.Lock()
	u.task = t
	u.saveLocked()
	u.mu.Unlock()
	log.Printf("updates: started %s (%s) %v", id, runner, args)
	return t, nil
}

// Task returns the current (or last) task with its state brought up to date.
func (u *Updates) Task(ctx context.Context) *Task {
	u.mu.Lock()
	t := u.task
	u.mu.Unlock()
	if t == nil || t.State != "running" {
		return t
	}
	b, err := u.hostFile(ctx, taskDir+"/"+t.ID+".exit")
	code, exited := 0, false
	if err == nil {
		code, exited = parseExit(b)
	}
	up := readUptime()
	cp := *t
	switch {
	case exited:
		cp.Exit, cp.Finished = &code, time.Now().Unix()
		cp.State = "done"
		if code != 0 {
			cp.State = "failed"
		}
	case t.Runner == "systemd" && u.unitStopped(ctx, t.ID) && !u.exitWritten(ctx, t.ID):
		cp.State, cp.Finished = "interrupted", time.Now().Unix() // the job was killed before it could report
	case up >= 0 && time.Now().Unix()-t.Started > int64(up)+60:
		cp.State, cp.Finished = "interrupted", time.Now().Unix() // the server rebooted meanwhile
	case t.Runner == "container" && !u.docker.Exists(ctx, helperPrefix+"task-"+t.ID):
		cp.State, cp.Finished = "interrupted", time.Now().Unix() // Docker restarted meanwhile
	default:
		return t
	}
	if cp.Runner == "container" {
		u.docker.RemoveContainer(context.Background(), helperPrefix+"task-"+cp.ID)
	}
	u.mu.Lock()
	changed := u.task != nil && u.task.ID == cp.ID && u.task.State == "running"
	if u.task != nil && u.task.ID == cp.ID {
		u.task = &cp
		u.saveLocked()
	}
	u.mu.Unlock()
	if changed && cp.Kind != "check" {
		switch cp.State {
		case "done":
			u.journal.Add("ok", upperFirst(cp.Title)+": готово")
		case "failed":
			u.journal.Add("bad", upperFirst(cp.Title)+": ошибка")
		default:
			u.journal.Add("warn", upperFirst(cp.Title)+": прервано")
		}
	}
	u.invalidate()
	if cp.Kind == "app" || cp.Kind == "git" {
		u.apps.Invalidate()
	}
	return &cp
}

// parseExit reads an exit file. done is false while it holds no number yet
// (a wrapper of an older version wrote it in place, so it may be caught
// empty); text that is not a number counts as a failure, never as success.
func parseExit(b []byte) (code int, done bool) {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return -1, true
	}
	return n, true
}

func (u *Updates) exitWritten(ctx context.Context, id string) bool {
	b, err := u.hostFile(ctx, taskDir+"/"+id+".exit")
	if err != nil {
		return false
	}
	_, done := parseExit(b)
	return done
}

// unitStopped says whether the systemd unit of a task is certainly gone: a
// wrapper killed (OOM, kill -9) leaves no exit file and would stay "running"
// forever. A failed or unclear look says false. The look costs a helper
// container, so it is taken at most every 20 seconds.
func (u *Updates) unitStopped(ctx context.Context, id string) bool {
	u.mu.Lock()
	if time.Since(u.unitAt) < 20*time.Second {
		u.mu.Unlock()
		return false
	}
	u.unitAt = time.Now()
	u.mu.Unlock()
	out, _, err := u.docker.HostRun(ctx, "systemctl", "is-active", "prichal-"+id)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(out) {
	case "inactive", "failed":
		return true
	}
	return false
}

// TaskLog returns the job output starting at byte offset.
func (u *Updates) TaskLog(ctx context.Context, offset int) (string, int) {
	u.mu.Lock()
	t := u.task
	u.mu.Unlock()
	if t == nil {
		return "", 0
	}
	b, err := u.hostFile(ctx, taskDir+"/"+t.ID+".log")
	if err != nil || offset < 0 || offset > len(b) {
		return "", len(b)
	}
	return string(b[offset:]), len(b)
}

func (u *Updates) Check(ctx context.Context) (*Task, error) {
	sys, err := u.System(ctx, false)
	if err != nil {
		return nil, err
	}
	if !sys.Supported {
		return nil, fmt.Errorf("пакетный менеджер %s пока не поддерживается", sys.PM)
	}
	u.apps.Invalidate()
	return u.Start(ctx, "check", "проверка обновлений", []string{sys.PM})
}

var pkgName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9+._\-]{0,127}$`)

// Install accepts only names that are in the current list of upgradable
// packages.
func (u *Updates) Install(ctx context.Context, names []string) (*Task, error) {
	sys, err := u.System(ctx, false)
	if err != nil {
		return nil, err
	}
	if !sys.Supported {
		return nil, fmt.Errorf("пакетный менеджер %s пока не поддерживается", sys.PM)
	}
	allowed := map[string]bool{}
	for _, p := range sys.Packages {
		allowed[p.Name] = true
	}
	var pkgs []string
	seen := map[string]bool{}
	for _, n := range names {
		if !pkgName.MatchString(n) || !allowed[n] {
			return nil, fmt.Errorf("пакета %q нет в списке обновлений, обновите страницу", n)
		}
		if !seen[n] {
			seen[n] = true
			pkgs = append(pkgs, n)
		}
	}
	if len(pkgs) == 0 {
		return nil, errors.New("ничего не выбрано")
	}
	title := fmt.Sprintf("установка %d %s", len(pkgs), plural(len(pkgs), "пакета", "пакетов", "пакетов"))
	return u.Start(ctx, "install", title, append([]string{sys.PM}, pkgs...))
}

// UpdateApp updates one docker compose service, optionally backing up its
// named volumes first.
func (u *Updates) UpdateApp(ctx context.Context, key string, backup bool) (*Task, error) {
	app, err := u.apps.Find(ctx, key)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, errors.New("приложение не найдено, обновите страницу")
	}
	if app.Local {
		return nil, errors.New("этот образ собран на сервере, его нечем сравнить и неоткуда скачать")
	}
	b := "0"
	if backup {
		b = "1"
	}
	args := []string{app.Project, app.Service, app.workDir, app.files, b, helperImage}
	for _, v := range app.Volumes {
		args = append(args, v.Name)
	}
	return u.Start(ctx, "app", "обновление "+app.Title, args)
}

func (u *Updates) Reboot(ctx context.Context) error {
	u.startMu.Lock()
	defer u.startMu.Unlock()
	if t := u.Task(ctx); t != nil && t.State == "running" {
		return errBusy
	}
	sys, err := u.System(ctx, false)
	if err != nil {
		return err
	}
	if sys.Init == "systemd" {
		// Delayed by a few seconds so the browser gets its answer first.
		out, code, err := u.docker.HostRun(ctx, "systemd-run", "--on-active=5", "--unit=prichal-reboot-"+time.Now().UTC().Format("150405"),
			"--description=Причал: перезагрузка", "/bin/sh", "-c", "systemctl reboot")
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("сервер не принял команду: %s", strings.TrimSpace(out))
		}
	} else if err := u.docker.HostStart(ctx, helperPrefix+"reboot", "/bin/sh", "-c", "sleep 5; reboot"); err != nil {
		return err
	}
	log.Printf("updates: reboot requested")
	u.journal.Add("", "Перезагрузка сервера из панели")
	return nil
}

// plural picks the Russian form for n: one, few, many.
func plural(n int, one, few, many string) string {
	a, b := n%10, n%100
	switch {
	case a == 1 && b != 11:
		return one
	case a >= 2 && a <= 4 && (b < 12 || b > 14):
		return few
	}
	return many
}
