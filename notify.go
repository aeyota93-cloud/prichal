package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the image has no zoneinfo of its own
)

// Notifier watches Docker events and the host and reports problems to
// Telegram and to the panel's timeline: crashes, restart loops, unhealthy
// containers, out-of-memory kills, recoveries, server reboots, low disk and
// low memory. Manual stops (a "kill" right before "die", which is what docker
// stop / the panel do) are not reported. Without Telegram it still fills the
// timeline.

// zone is the time zone of the messages and of the weekly digest: TZ from
// .env, Moscow by default. zoneName is how the messages write it.
var zone, zoneName = loadZone(os.Getenv("TZ"))

func loadZone(name string) (*time.Location, string) {
	if name == "" || name == "Europe/Moscow" {
		if loc, err := time.LoadLocation("Europe/Moscow"); err == nil {
			return loc, "МСК"
		}
		return time.FixedZone("МСК", 3*3600), "МСК"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("TZ=%q: %v, беру московское время", name, err)
		return time.FixedZone("МСК", 3*3600), "МСК"
	}
	abbr, _ := time.Now().In(loc).Zone()
	return loc, abbr
}

const (
	manualWindow   = 30 * time.Second // kill -> die within this = stopped on purpose
	crashSettle    = 20 * time.Second // wait before reporting, Docker may restart it
	loopWindow     = 10 * time.Minute
	loopThreshold  = 3
	quietSameEvent = 10 * time.Minute
)

type Notifier struct {
	docker   *Docker
	tg       *Telegram
	hostRoot string
	upd      *Updates
	journal  *Journal

	mu       sync.Mutex
	lastKill map[string]time.Time   // container id -> last kill event
	crashes  map[string][]time.Time // container id -> recent crash times
	down     map[string]bool        // we reported a problem, report recovery
	sent     map[string]time.Time   // dedupe key -> when
}

func NewNotifier(d *Docker, tg *Telegram, hostRoot string, upd *Updates, journal *Journal) *Notifier {
	return &Notifier{
		docker: d, tg: tg, hostRoot: hostRoot, upd: upd, journal: journal,
		lastKill: map[string]time.Time{}, crashes: map[string][]time.Time{},
		down: map[string]bool{}, sent: map[string]time.Time{},
	}
}

// nice is the bold human name of a container for a message.
func nice(name, image, service string) string {
	return "<b>" + html.EscapeString(identify(name, image, service, false).Title) + "</b>"
}

// send delivers text to Telegram unless the same key was sent recently.
// It reports whether the event is new.
func (n *Notifier) send(key, text string) bool {
	n.mu.Lock()
	if t, ok := n.sent[key]; ok && time.Since(t) < quietSameEvent {
		n.mu.Unlock()
		return false
	}
	n.sent[key] = time.Now()
	n.mu.Unlock()
	log.Printf("notify: %s", key)
	if n.tg != nil {
		n.tg.Send(text)
	}
	return true
}

// report is send plus a short plain line for the panel's timeline.
func (n *Notifier) report(key, tone, short, text string) {
	if n.send(key, text) {
		n.journal.Add(tone, short)
	}
}

func (n *Notifier) Run(ctx context.Context) {
	if n.tg != nil {
		go n.tg.Run(ctx, func() { n.welcome(ctx) })
	}
	go n.bootCheck(ctx)
	go n.hostLoop(ctx)
	if n.upd != nil && n.tg != nil {
		go n.digestLoop(ctx)
	}
	n.eventsLoop(ctx)
}

// ---------- Docker events ----------

type dockerEvent struct {
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
}

func (n *Notifier) eventsLoop(ctx context.Context) {
	filters := `{"type":["container"],"event":["kill","die","oom","start","health_status"]}`
	for ctx.Err() == nil {
		resp, err := n.docker.do(ctx, "GET", "/events", url.Values{"filters": {filters}})
		if err != nil {
			log.Printf("notify: events: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var ev dockerEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				n.handle(ctx, ev)
			}
		}
		resp.Body.Close()
		time.Sleep(3 * time.Second)
	}
}

func (n *Notifier) handle(ctx context.Context, ev dockerEvent) {
	id, raw := ev.Actor.ID, ev.Actor.Attributes["name"]
	if isHelper(raw) {
		return
	}
	a := ev.Actor.Attributes
	name := nice(raw, a["image"], a["com.docker.compose.service"])
	title := identify(raw, a["image"], a["com.docker.compose.service"], false).Title
	now := time.Now()
	switch {
	case ev.Action == "kill":
		n.mu.Lock()
		n.lastKill[id] = now
		n.mu.Unlock()

	case ev.Action == "oom":
		n.mu.Lock()
		n.down[id] = true
		n.mu.Unlock()
		n.report("oom/"+id, "bad", title+": не хватило памяти", fmt.Sprintf("🔴 %s: не хватило памяти, Docker выключил процесс внутри.", name))

	case ev.Action == "die":
		n.mu.Lock()
		// Exit code 0 is a normal finish (one-off jobs), not a crash.
		manual := now.Sub(n.lastKill[id]) < manualWindow || ev.Actor.Attributes["exitCode"] == "0"
		delete(n.lastKill, id)
		if manual {
			n.mu.Unlock()
			return
		}
		recent := n.crashes[id][:0]
		for _, t := range n.crashes[id] {
			if now.Sub(t) < loopWindow {
				recent = append(recent, t)
			}
		}
		recent = append(recent, now)
		n.crashes[id] = recent
		loop := len(recent) >= loopThreshold
		n.down[id] = true
		n.mu.Unlock()
		code := ev.Actor.Attributes["exitCode"]
		if loop {
			n.report("loop/"+id, "bad", title+" падает снова и снова", fmt.Sprintf("🔴 %s падает снова и снова: %d раза за %d минут. Последний код выхода %s. Загляните в журнал в «Причале».",
				name, len(recent), int(loopWindow.Minutes()), html.EscapeString(code)))
			return
		}
		go n.afterCrash(ctx, id, name, title, code)

	case ev.Action == "start":
		n.mu.Lock()
		wasDown := n.down[id]
		n.mu.Unlock()
		if wasDown {
			// Recovery is confirmed a bit later so a restart loop doesn't
			// produce "works again" every few seconds.
			go n.confirmRecovery(ctx, id, name, title)
		}

	case strings.HasPrefix(ev.Action, "health_status"):
		status := strings.TrimSpace(strings.TrimPrefix(ev.Action, "health_status:"))
		n.mu.Lock()
		wasDown := n.down[id]
		if status == "unhealthy" {
			n.down[id] = true
		}
		n.mu.Unlock()
		switch {
		case status == "unhealthy":
			n.report("unhealthy/"+id, "warn", title+" нездоров", fmt.Sprintf("🟠 %s нездоров: работает, но не проходит собственную проверку.", name))
		case status == "healthy" && wasDown:
			go n.confirmRecovery(ctx, id, name, title)
		}
	}
}

func (n *Notifier) afterCrash(ctx context.Context, id, name, title, code string) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(crashSettle):
	}
	in, err := n.docker.Inspect(ctx, id)
	if err != nil {
		n.report("crash/"+id, "bad", title+" упал и, похоже, удалён", fmt.Sprintf("🔴 %s упал (код выхода %s) и, похоже, удалён.", name, html.EscapeString(code)))
		return
	}
	if in.State.Running {
		n.report("crash/"+id, "warn", title+" упал, Docker сам его перезапустил", fmt.Sprintf("🟠 %s упал (код выхода %s). Docker сам его перезапустил, сейчас работает.", name, html.EscapeString(code)))
		n.mu.Lock()
		delete(n.down, id) // already fine, no separate recovery message
		n.mu.Unlock()
		return
	}
	n.report("crash/"+id, "bad", title+" упал и не работает", fmt.Sprintf("🔴 %s упал (код выхода %s) и не работает. Запустить его можно в «Причале».", name, html.EscapeString(code)))
}

func (n *Notifier) confirmRecovery(ctx context.Context, id, name, title string) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(90 * time.Second):
	}
	in, err := n.docker.Inspect(ctx, id)
	if err != nil || !in.State.Running || (in.State.Health != nil && in.State.Health.Status == "unhealthy") {
		return
	}
	n.mu.Lock()
	wasDown := n.down[id]
	delete(n.down, id)
	n.mu.Unlock()
	if wasDown {
		n.report("ok/"+id, "ok", title+" снова работает", fmt.Sprintf("🟢 %s снова работает.", name))
	}
}

// forget drops what no longer matters, so the maps do not grow forever
// with IDs of containers that are long gone.
func (n *Notifier) forget(now time.Time) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, t := range n.sent {
		if now.Sub(t) >= quietSameEvent {
			delete(n.sent, k)
		}
	}
	for id, t := range n.lastKill {
		if now.Sub(t) >= manualWindow {
			delete(n.lastKill, id)
		}
	}
	for id, ts := range n.crashes {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= loopWindow {
			delete(n.crashes, id)
		}
	}
}

// ---------- Server ----------

func readUptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return -1
	}
	var up float64
	fmt.Sscanf(string(b), "%f", &up)
	return up
}

func readKernel() string {
	b, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	return strings.TrimSpace(string(b))
}

// containerSummary: "работают 6 из 6" plus names of the ones that are not.
func (n *Notifier) containerSummary(ctx context.Context) string {
	list, err := n.docker.Containers(ctx)
	if err != nil {
		return "Не удалось спросить Docker о контейнерах."
	}
	var bad []string
	for _, c := range list {
		if c.State != "running" {
			bad = append(bad, nice(c.Name(), c.Image, c.Labels["com.docker.compose.service"]))
		}
	}
	sort.Strings(bad)
	if len(bad) == 0 {
		return fmt.Sprintf("Всё работает: %d из %d контейнеров.", len(list), len(list))
	}
	return fmt.Sprintf("⚠️ Не работают: %s. Остальные %d из %d в порядке.", strings.Join(bad, ", "), len(list)-len(bad), len(list))
}

func (n *Notifier) bootCheck(ctx context.Context) {
	kernel := readKernel()
	prev := ""
	if n.tg != nil {
		prev = n.tg.SwapKernel(kernel)
	}
	up := readUptime()
	if up < 0 || up > 15*60 {
		return // the panel restarted, not the server
	}
	// Give everything a few minutes to come up before judging.
	wait := 3*time.Minute - time.Duration(up)*time.Second
	if wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	bootAt := time.Now().Add(-time.Duration(readUptime()) * time.Second).In(zone)
	msg := fmt.Sprintf("🔄 Сервер перезагрузился в %s %s.", bootAt.Format("15:04"), zoneName)
	short := "Сервер перезагрузился"
	if prev != "" && prev != kernel {
		short += ", новое ядро " + kernel
		msg += fmt.Sprintf("\nЯдро обновлено: %s → %s.", html.EscapeString(prev), html.EscapeString(kernel))
	}
	msg += "\n" + n.containerSummary(ctx)
	n.report("boot", "", short, msg)
}

func (n *Notifier) hostLoop(ctx context.Context) {
	var diskWarned, memWarned bool
	lowMem := 0
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		n.forget(time.Now())
		if total, used := diskUsage(n.hostRoot); total > 0 {
			pct := float64(used) / float64(total) * 100
			switch {
			case pct >= 85 && !diskWarned:
				diskWarned = true
				n.report("disk", "warn", fmt.Sprintf("Диск заполнен на %.0f%%", pct), fmt.Sprintf("🟠 Диск заполнен на %.0f%%: свободно %s. Очистить неиспользуемое можно в «Причале», раздел «Образы».", pct, humanBytes(total-used)))
			case pct < 80 && diskWarned:
				diskWarned = false
				n.report("disk-ok", "ok", "Место на диске освободилось", fmt.Sprintf("🟢 Место на диске освободилось: занято %.0f%%.", pct))
			}
		}
		if mem, err := readKV("/proc/meminfo"); err == nil && mem["MemTotal"] > 0 {
			free := float64(mem["MemAvailable"]) / float64(mem["MemTotal"]) * 100
			if free < 8 {
				lowMem++
			} else {
				lowMem = 0
			}
			switch {
			case lowMem >= 3 && !memWarned:
				memWarned = true
				n.report("mem", "warn", "Заканчивается память", fmt.Sprintf("🟠 Заканчивается память: свободно %.0f%% уже несколько минут. Кто сколько ест, видно в «Причале».", free))
			case free > 15 && memWarned:
				memWarned = false
				n.report("mem-ok", "ok", "С памятью снова всё нормально", "🟢 С памятью снова всё нормально.")
			}
		}
	}
}

// ---------- Weekly updates digest (Sundays, 12:00 local time) ----------

// lastDigestSlot is the most recent Sunday 12:00 (in zone) not later than now.
func lastDigestSlot(now time.Time) time.Time {
	m := now.In(zone)
	slot := time.Date(m.Year(), m.Month(), m.Day(), 12, 0, 0, 0, zone).AddDate(0, 0, -int(m.Weekday()))
	if slot.After(m) {
		slot = slot.AddDate(0, 0, -7)
	}
	return slot
}

func (n *Notifier) digestLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute): // let a fresh boot settle first
	}
	for {
		slot := lastDigestSlot(time.Now())
		// Only within a day after the slot, so a panel restart on Wednesday
		// does not send last Sunday's digest.
		if time.Since(slot) < 24*time.Hour && n.tg.DigestDue(slot) {
			if msg := n.digest(ctx); msg != "" {
				n.send("digest", msg)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (n *Notifier) digest(ctx context.Context) string {
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	v := n.upd.View(dctx, true)
	var lines []string
	if sys := v.System; sys != nil && len(sys.Packages) > 0 {
		groups := map[string]int{}
		for _, p := range sys.Packages {
			groups[p.Group]++
		}
		line := fmt.Sprintf("Пакеты системы: %d", len(sys.Packages))
		var parts []string
		for _, g := range []struct{ key, name string }{{"security", "безопасность"}, {"docker", "Docker"}, {"kernel", "ядро"}} {
			if groups[g.key] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", g.name, groups[g.key]))
			}
		}
		if len(parts) > 0 {
			line += " (" + strings.Join(parts, ", ") + ")"
		}
		lines = append(lines, line+".")
	}
	if v.Apps != nil {
		for _, a := range v.Apps.Apps {
			if !a.Update {
				continue
			}
			what := "новая сборка"
			if a.Git != nil {
				what = fmt.Sprintf("новая версия в git (%d %s)", a.Git.Behind, plural(a.Git.Behind, "коммит", "коммита", "коммитов"))
			}
			if a.Latest != "" && a.Latest != a.Current {
				what = "версия " + a.Latest
				if a.Current != "" {
					what += ", у вас " + a.Current
				}
			}
			lines = append(lines, fmt.Sprintf("%s: %s.", html.EscapeString(a.Title), html.EscapeString(what)))
		}
	}
	if v.System != nil && v.System.RebootRequired {
		lines = append(lines, "Серверу нужна перезагрузка.")
	}
	if len(lines) == 0 {
		return ""
	}
	return "📦 <b>Обновления за неделю</b>\n" + strings.Join(lines, "\n") + "\nУстановить можно в «Причале», раздел «Обновления»."
}

func (n *Notifier) welcome(ctx context.Context) {
	n.send("welcome", "Привет! Это «Причал» с вашего сервера. Буду писать сюда, если контейнер упадёт, сервер перезагрузится или начнёт заканчиваться место.\n\n"+n.containerSummary(ctx))
}

func humanBytes(b uint64) string {
	f := float64(b)
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", f, units[i]), ".", ",", 1)
}
