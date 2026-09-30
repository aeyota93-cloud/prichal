package main

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Apps are docker compose services. For each one the panel checks whether the
// registry has a newer image under the same tag and can update it: pull,
// stop, back up its named volumes, start again. Containers created without
// compose (Amnezia, plain `docker run`) are listed but not updated from here.

const (
	appsCacheFor     = 6 * time.Hour
	backupDefaultMax = 1 << 30 // volumes above 1 GB are not backed up by default
)

type AppVolume struct {
	Name string `json:"name"`
	Size int64  `json:"size"` // -1 when unknown
}

type App struct {
	Key       string      `json:"key"` // project/service
	Project   string      `json:"project"`
	Service   string      `json:"service"`
	Container string      `json:"container"`
	Title     string      `json:"title"`
	Image     string      `json:"image"`
	Current   string      `json:"current"` // version label, if the image has one
	Latest    string      `json:"latest"`
	Built     string      `json:"built,omitempty"` // build date of the newer image
	Update    bool        `json:"update"`
	Pinned    bool        `json:"pinned"`
	Local     bool        `json:"local"` // built on this server, nothing to compare with
	Self      bool        `json:"self"`
	Volumes   []AppVolume `json:"volumes"`
	Error     string      `json:"error,omitempty"`

	workDir, files string
}

type ManualApp struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type AppsView struct {
	Apps      []App       `json:"apps"`
	Manual    []ManualApp `json:"manual"`
	CheckedAt int64       `json:"checkedAt"`
}

type Apps struct {
	docker *Docker
	reg    *registry
	selfID string

	mu   sync.Mutex
	view *AppsView
	at   time.Time
}

func NewApps(d *Docker, selfID string) *Apps {
	return &Apps{docker: d, reg: newRegistry(), selfID: selfID}
}

func (a *Apps) Invalidate() {
	a.mu.Lock()
	a.view = nil
	a.mu.Unlock()
}

// Find returns the app with key project/service from the last listing.
func (a *Apps) Find(ctx context.Context, key string) (*App, error) {
	v, err := a.View(ctx, false)
	if err != nil {
		return nil, err
	}
	for i := range v.Apps {
		if v.Apps[i].Key == key {
			return &v.Apps[i], nil
		}
	}
	return nil, nil
}

func (a *Apps) View(ctx context.Context, fresh bool) (*AppsView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !fresh && a.view != nil && time.Since(a.at) < appsCacheFor {
		return a.view, nil
	}
	v, err := a.collect(ctx)
	if err != nil {
		return nil, err
	}
	a.view, a.at = v, time.Now()
	return v, nil
}

type imageInfo struct {
	RepoDigests []string `json:"RepoDigests"`
	Config      struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

type containerMounts struct {
	Config struct {
		Image string `json:"Image"` // the reference as written in compose
	} `json:"Config"`
	Image  string `json:"Image"` // image ID
	Mounts []struct {
		Type string `json:"Type"`
		Name string `json:"Name"`
	} `json:"Mounts"`
}

func (a *Apps) collect(ctx context.Context) (*AppsView, error) {
	list, err := a.docker.Containers(ctx)
	if err != nil {
		return nil, err
	}
	sizes := a.volumeSizes(ctx)
	v := &AppsView{Apps: []App{}, Manual: []ManualApp{}, CheckedAt: time.Now().Unix()}
	seen := map[string]bool{}
	for _, c := range list {
		name := c.Name()
		if isHelper(name) {
			continue
		}
		self := a.selfID != "" && strings.HasPrefix(c.ID, a.selfID)
		project, service := c.Labels["com.docker.compose.project"], c.Labels["com.docker.compose.service"]
		id := identify(name, c.Image, service, self)
		if project == "" || c.Labels["com.docker.compose.project.working_dir"] == "" {
			reason := "создан без docker compose, обновляется вручную"
			if strings.HasPrefix(name, "amnezia-") {
				reason = "обновляется из приложения Amnezia"
			}
			v.Manual = append(v.Manual, ManualApp{Name: name, Title: id.Title, Reason: reason})
			continue
		}
		key := project + "/" + service
		if seen[key] {
			continue // replicas of the same service
		}
		seen[key] = true
		app := App{
			Key: key, Project: project, Service: service, Container: name, Title: id.Title, Self: self,
			Volumes: []AppVolume{},
			workDir: c.Labels["com.docker.compose.project.working_dir"],
			files:   c.Labels["com.docker.compose.project.config_files"],
		}
		var cm containerMounts
		if err := a.docker.getJSON(ctx, "/containers/"+c.ID+"/json", nil, &cm); err != nil {
			app.Error = err.Error()
			v.Apps = append(v.Apps, app)
			continue
		}
		app.Image = cm.Config.Image
		for _, m := range cm.Mounts {
			if m.Type == "volume" && m.Name != "" {
				size, ok := sizes[m.Name]
				if !ok {
					size = -1
				}
				app.Volumes = append(app.Volumes, AppVolume{Name: m.Name, Size: size})
			}
		}
		var img imageInfo
		if err := a.docker.getJSON(ctx, "/images/"+url.PathEscape(cm.Image)+"/json", nil, &img); err == nil {
			app.Current = img.Config.Labels["org.opencontainers.image.version"]
			app.Local = len(img.RepoDigests) == 0
		}
		if !app.Local {
			ref := parseRef(app.Image)
			app.Pinned = ref.pinned()
			if ref.Digest == "" { // pinned by digest: nothing can change
				app.localDigests(img.RepoDigests)
			}
		}
		v.Apps = append(v.Apps, app)
	}
	a.checkRemote(ctx, v)
	sort.SliceStable(v.Apps, func(i, j int) bool {
		if v.Apps[i].Update != v.Apps[j].Update {
			return v.Apps[i].Update
		}
		return v.Apps[i].Title < v.Apps[j].Title
	})
	sort.SliceStable(v.Manual, func(i, j int) bool { return v.Manual[i].Name < v.Manual[j].Name })
	return v, nil
}

// localDigests remembers which digests this server has for the image; kept
// in Latest temporarily until checkRemote compares them.
func (app *App) localDigests(repoDigests []string) {
	var ds []string
	for _, rd := range repoDigests {
		if i := strings.Index(rd, "@"); i >= 0 {
			ds = append(ds, rd[i+1:])
		}
	}
	app.Latest = strings.Join(ds, " ")
}

func (a *Apps) checkRemote(ctx context.Context, v *AppsView) {
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i := range v.Apps {
		app := &v.Apps[i]
		if app.Local || app.Error != "" || app.Image == "" || parseRef(app.Image).Digest != "" {
			app.Latest = ""
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			local := strings.Fields(app.Latest)
			app.Latest = ""
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			ref := parseRef(app.Image)
			remote, err := a.reg.Digest(rctx, ref)
			if errors.Is(err, errNotInRegistry) {
				app.Local = true // built on the server or private: nothing to compare with
				return
			}
			if err != nil {
				app.Error = "не удалось проверить: " + err.Error()
				return
			}
			app.Update = true
			for _, d := range local {
				if d == remote {
					app.Update = false
				}
			}
			if app.Update {
				app.Latest, app.Built = a.reg.Info(rctx, ref, remote)
				if len(app.Built) >= 10 {
					app.Built = app.Built[:10]
				}
			} else {
				app.Latest = app.Current
			}
		}()
	}
	wg.Wait()
}

func (a *Apps) volumeSizes(ctx context.Context) map[string]int64 {
	var df struct {
		Volumes []struct {
			Name      string `json:"Name"`
			UsageData struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	out := map[string]int64{}
	if a.docker.getJSON(ctx, "/system/df", url.Values{"type": {"volume"}}, &df) == nil {
		for _, v := range df.Volumes {
			out[v.Name] = v.UsageData.Size
		}
	}
	return out
}

// appUpdateScript updates one compose service. Positional arguments:
// project, service, working dir, compose files (comma separated), backup
// (1/0), helper image, then the volumes to back up.
const appUpdateScript = `project=$1; svc=$2; wd=$3; files=$4; backup=$5; helper=$6; shift 6
cd "$wd"
fargs=""
old_ifs=$IFS; IFS=,
for f in $files; do fargs="$fargs -f $f"; done
IFS=$old_ifs
dc() { docker compose -p "$project" $fargs "$@"; }
echo "== Скачиваю новую версию ($svc)"
dc pull "$svc"
ok=0
trap '[ "$ok" = 1 ] || { echo "!! Ошибка. Запускаю $svc обратно"; dc up -d "$svc"; }' EXIT
if [ "$backup" = 1 ] && [ $# -gt 0 ]; then
  echo "== Останавливаю $svc, чтобы сделать копию данных"
  dc stop "$svc"
  dir=/var/backups/prichal/$project/$(date +%Y%m%d-%H%M%S)
  mkdir -p "$dir"
  for v in "$@"; do
    echo "   копирую том $v"
    docker run --rm --name "prichal-host-backup-$$" -v "$v":/data:ro -v "$dir":/backup "$helper" tar czf "/backup/$v.tar.gz" -C /data .
  done
  for f in $(echo "$files" | tr , ' '); do cp "$f" "$dir"/ 2>/dev/null || true; done
  echo "== Копия данных: $dir"
fi
echo "== Запускаю новую версию"
dc up -d "$svc"
ok=1
docker image prune -f >/dev/null 2>&1 || true
ls -1dt /var/backups/prichal/"$project"/*/ 2>/dev/null | tail -n +6 | xargs -r rm -rf
echo "== Готово. Хранятся 5 последних копий в /var/backups/prichal/$project"`
