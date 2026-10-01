package main

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The Images page: what takes space, what can go, and the cleanup.

type imageView struct {
	ID      string   `json:"id"`
	Tags    []string `json:"tags"`
	Size    int64    `json:"size"`
	Created int64    `json:"created"`
	UsedBy  []string `json:"usedBy"`
}

func (s *server) imageViews(ctx context.Context) ([]imageView, int64, error) {
	imgs, err := s.docker.Images(ctx)
	if err != nil {
		return nil, 0, err
	}
	conts, err := s.docker.Containers(ctx)
	if err != nil {
		return nil, 0, err
	}
	used := map[string][]string{}
	for _, c := range conts {
		used[c.ImageID] = append(used[c.ImageID], c.Name())
	}
	out := make([]imageView, 0, len(imgs))
	for _, im := range imgs {
		tags := []string{}
		for _, t := range im.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		u := used[im.ID]
		if u == nil {
			u = []string{}
		}
		if isHelperImage(im) {
			// Runs host commands for the Updates tab. Pulled by digest, it
			// has no tag of its own.
			if len(u) == 0 {
				u = []string{"prichal"}
			}
			if len(tags) == 0 {
				tags = []string{helperName}
			}
		}
		out = append(out, imageView{ID: im.ID, Tags: tags, Size: im.Size, Created: im.Created, UsedBy: u})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Size > out[b].Size })

	var cache int64
	if du, err := s.docker.DiskUsage(ctx); err == nil {
		cache = du.ReclaimableCache()
	}
	return out, cache, nil
}

func isHelperImage(im ImageSummary) bool {
	for _, d := range im.RepoDigests {
		if strings.TrimPrefix(strings.TrimPrefix(d, "docker.io/"), "library/") == helperImage {
			return true
		}
	}
	return false
}

func (s *server) images(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 20*time.Second)
	defer cancel()
	imgs, cache, err := s.imageViews(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"images": imgs, "buildCache": cache})
}

func (s *server) removeImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, cancel := ctxTimeout(r, 60*time.Second)
	defer cancel()
	imgs, _, err := s.imageViews(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, im := range imgs {
		if im.ID != id {
			continue
		}
		if len(im.UsedBy) > 0 {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "образ используется контейнером " + strings.Join(im.UsedBy, ", ")})
			return
		}
		log.Printf("remove image %s %v", id, im.Tags)
		if err := s.docker.RemoveImage(ctx, id, im.Tags); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "образ не найден"})
}

// prune removes exactly what the Images page offers: images no container
// needs (the helper image for host commands stays, Docker's own image prune
// would take it) and the build cache. The freed space is measured, not
// estimated.
func (s *server) prune(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxTimeout(r, 5*time.Minute)
	defer cancel()
	log.Printf("prune unused images and build cache")
	imgs, _, err := s.imageViews(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	before, errBefore := s.docker.DiskUsage(ctx)
	for _, im := range imgs {
		if len(im.UsedBy) > 0 {
			continue
		}
		if err := s.docker.RemoveImage(ctx, im.ID, im.Tags); err != nil {
			log.Printf("prune: image %s %v: %v", im.ID, im.Tags, err)
		}
	}
	n, err := s.docker.PruneBuildCache(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Docker's own count includes cache entries shared with image layers,
	// which stay on disk. What the page promised is measured instead.
	if after, err := s.docker.DiskUsage(ctx); err == nil && errBefore == nil {
		n = max(0, before.ImagesSize()-after.ImagesSize()) + max(0, before.ReclaimableCache()-after.ReclaimableCache())
	}
	writeJSON(w, http.StatusOK, map[string]int64{"reclaimed": n})
}
