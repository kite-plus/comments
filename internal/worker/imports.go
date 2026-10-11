package worker

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/kite-plus/comments/internal/crypt"
	"github.com/kite-plus/comments/internal/importer"
	"github.com/kite-plus/comments/internal/markup"
	"github.com/kite-plus/comments/internal/store"
)

// runImports runs queued imports one at a time (sites.md section 7, step
// 4). Guest addresses are encrypted as they come in; IPs and user agents
// were never read.
func (w *Worker) runImports(ctx context.Context) {
	for range 5 {
		im, err := w.Store.NextImport(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			w.Log.Error("import", "error", err)
			return
		}
		stats, failed := w.runImport(ctx, im)
		raw, _ := json.Marshal(stats)
		if err := w.Store.FinishImport(ctx, im.ID, raw, failed); err != nil {
			w.Log.Error("import", "error", err)
			return
		}
		w.Log.Info("import finished", "id", im.ID, "failed", failed != "")
	}
}

func (w *Worker) runImport(ctx context.Context, im store.Import) (map[string]any, string) {
	var previous map[string]any
	_ = json.Unmarshal(im.Stats, &previous)
	out := map[string]any{"preview": previous["preview"]}
	file, err := w.Store.ImportFile(ctx, im.ID)
	if err != nil {
		return out, "the uploaded file is gone"
	}
	site, err := w.Store.SiteByID(ctx, im.SiteID)
	if err != nil {
		return out, "the site is gone"
	}
	var m importer.Mapping
	_ = json.Unmarshal(im.Mapping, &m)
	r, err := importer.Parse(im.Format, file)
	if err != nil {
		return out, err.Error()
	}
	var origins []string
	for _, o := range site.Origins {
		origins = append(origins, o.Origin)
	}
	plan, _ := importer.MakePlan(r, m, importer.Site{Origins: origins, Primary: site.PrimaryOrigin(), QueryKeys: site.QueryKeys})
	threads := make([]store.ImportThread, 0, len(plan.Threads))
	for _, t := range plan.Threads {
		threads = append(threads, store.ImportThread{Key: t.Key, URL: t.URL, Title: t.Title, PublishedAt: t.PublishedAt, Locked: t.Locked})
	}
	comments := make([]store.ImportComment, 0, len(plan.Comments))
	for _, c := range plan.Comments {
		ic := store.ImportComment{SourceID: c.SourceID, ParentSourceID: c.ParentSourceID, RootSourceID: c.RootSourceID,
			ThreadKey: c.Key, AuthorKind: importer.AuthorKind(c.Comment), AuthorName: importer.AuthorName(c.Comment),
			AuthorURL: webURL(c.AuthorURL), AuthorRef: limit(c.AuthorRef, 100), BodyMD: limit(c.BodyMD, 10000),
			RenderVersion: markup.Version, Status: c.Status, Pinned: c.Pinned, CreatedAt: c.CreatedAt, EditedAt: c.EditedAt}
		ic.BodyHTML = markup.Render(ic.BodyMD).HTML
		if c.Email != "" {
			ic.EmailEnc, ic.EmailHMAC = w.Keys.Encrypt(crypt.NormalizeEmail(c.Email)), w.Keys.EmailHMAC(c.Email)
		}
		comments = append(comments, ic)
	}
	st, err := w.Store.ImportComments(ctx, site.ID, im.Format, threads, comments)
	if err != nil {
		return out, err.Error()
	}
	out["inserted"], out["skipped"], out["failed"] = st.Inserted, st.Skipped, st.Failed+len(r.Failures)
	out["unmatched"] = len(r.Comments) - len(plan.Comments)
	return out, ""
}

// webURL keeps an imported homepage only when it is an http or https
// address that fits the column; other systems did not always check.
func webURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len([]rune(raw)) > 300 {
		return ""
	}
	return raw
}

func limit(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
