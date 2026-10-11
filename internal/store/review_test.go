package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/model"
)

// An owner cannot shed a platform suspension by deleting and restoring.
func TestSuspensionSurvivesDeleteAndRestore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	site := newSite(t, s, "https://a.example", &owner)
	if err := s.SuspendSite(ctx, site.ID, "abuse"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteSuspended || x.StatusNote != "abuse" {
		t.Fatalf("restored = %s %q", x.Status, x.StatusNote)
	}
	if err := s.UnsuspendSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteActive || x.StatusNote != "" {
		t.Errorf("unsuspended = %s %q", x.Status, x.StatusNote)
	}
	// A site that was never suspended comes back as it was.
	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteActive {
		t.Errorf("restored again = %s", x.Status)
	}
}

// A ban names the identity, so deleting the account does not lift it.
func TestBansSurviveAccountDeletion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ann := newUser(t, s, "ann", "Ann")
	site := newSite(t, s, "https://a.example", nil)
	iss, sub, err := s.Identity(ctx, ann.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBan(ctx, Ban{SiteID: &site.ID, Issuer: iss, Subject: sub, Reason: "spam"}, ann.ID); err != nil {
		t.Fatal(err)
	}
	if banned, err := s.Banned(ctx, site.ID, &ann.ID, nil, nil); err != nil || !banned {
		t.Fatalf("ban not applied: %v %v", banned, err)
	}
	if err := s.DeleteUserData(ctx, ann.ID, "remove", false); err != nil {
		t.Fatal(err)
	}
	again, created, err := s.LoginUser(ctx, OIDCLogin{Issuer: iss, Subject: sub, DisplayName: "Ann", Locale: "en"})
	if err != nil || !created || again.ID == ann.ID {
		t.Fatalf("sign-in after deletion = %+v %v %v", again, created, err)
	}
	if banned, err := s.Banned(ctx, site.ID, &again.ID, nil, nil); err != nil || !banned {
		t.Errorf("ban lifted by deletion: %v %v", banned, err)
	}
	bob := newUser(t, s, "bob", "Bob")
	if banned, _ := s.Banned(ctx, site.ID, &bob.ID, nil, nil); banned {
		t.Error("another reader banned")
	}
}

// An address the identity service no longer vouches for leaves the account.
func TestSignInWithoutAVerifiedEmailClearsIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newUser(t, s, "ann", "Ann")
	if u.EmailHMAC == nil {
		t.Fatal("no email to start with")
	}
	again, _, err := s.LoginUser(ctx, OIDCLogin{Issuer: "https://id.example", Subject: "ann", DisplayName: "Ann", Locale: "en"})
	if err != nil || again.EmailEnc != nil || again.EmailHMAC != nil {
		t.Errorf("email after an unverified sign-in = %v %v %v", again.EmailEnc, again.EmailHMAC, err)
	}
}

// A verified claim is confirmed once, and only takes comments written
// before it was verified.
func TestClaimAppliesOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ann := newUser(t, s, "ann", "Ann")
	site := newSite(t, s, "https://a.example", nil)
	guest := func(body string) model.Comment {
		c, err := s.CreateComment(ctx, NewComment{SiteID: site.ID, ThreadKey: "p:/x", ThreadURL: "https://a.example/x", AuthorKind: model.AuthorGuest,
			AuthorName: "G", AuthorEmailEnc: []byte("enc"), AuthorEmailHMAC: hash("g@x"), BodyMD: body, BodyHTML: body, RenderVersion: 1,
			Status: model.StatusPublished})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	guest("before")
	s.exec(t, `UPDATE comments SET created_at = now() - interval '1 hour'`)
	id, err := s.CreateClaim(ctx, ann.ID, hash("g@x"), hash("tok"), time.Now().Add(time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.ConfirmClaim(ctx, id, ann.ID); err != nil || n != 1 {
		t.Fatalf("confirm = %d %v", n, err)
	}
	later := guest("after")
	if _, err := s.ConfirmClaim(ctx, id, ann.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second confirm: %v", err)
	}
	if c, _ := s.Comment(ctx, later.ID); c.AuthorKind != model.AuthorGuest {
		t.Errorf("a later guest comment was claimed: %s", c.AuthorKind)
	}
}

// Imported comments age from the import, so old spam can still be
// reviewed and restored after the cleanup runs.
func TestImportedSpamCanBeRestored(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	site := newSite(t, s, "https://a.example", nil)
	old := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.ImportComments(ctx, site.ID, "waline", []ImportThread{{Key: "p:/x", URL: "https://a.example/x"}},
		[]ImportComment{{SourceID: "1", ThreadKey: "p:/x", AuthorKind: model.AuthorGuest, AuthorName: "G", BodyMD: "maybe ham",
			BodyHTML: "<p>maybe ham</p>", RenderVersion: 1, Status: model.StatusSpam, CreatedAt: old}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ImportedCount(ctx, site.ID); err != nil || n != 1 {
		t.Fatalf("imported count = %d %v", n, err)
	}
	if _, err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var id int64
	var created time.Time
	if err := s.pool.QueryRow(ctx, `SELECT id, created_at FROM comments WHERE site_id = $1`, site.ID).Scan(&id, &created); err != nil {
		t.Fatal(err)
	}
	if !created.Equal(old) {
		t.Errorf("created_at = %v", created)
	}
	c, err := s.Moderate(ctx, ModerateIn{SiteID: site.ID, CommentID: id, Action: "restore"})
	if err != nil || c.Status != model.StatusPublished || c.BodyMD != "maybe ham" {
		t.Errorf("restored = %+v %v", c, err)
	}
}

// The stored import goes a day after the upload, whatever became of it.
func TestImportFilesGoAfterADay(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	site := newSite(t, s, "https://a.example", &owner)
	id, err := s.CreateImport(ctx, site.ID, "waline", []byte(`{"Comments":[]}`), owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportFile(ctx, id); err != nil {
		t.Fatalf("fresh import cleared: %v", err)
	}
	s.exec(t, `UPDATE imports SET created_at = now() - interval '25 hours'`)
	if _, err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportFile(ctx, id); err == nil {
		t.Error("an unfinished import kept its file past a day")
	}
}

// Claims that were never verified expire with their token.
func TestUnverifiedSitesAndOriginsExpire(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	squat, err := s.CreateSite(ctx, NewSite{Name: "S", Origins: []string{"https://b.example"}, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.CreateSite(ctx, NewSite{Name: "F", Origins: []string{"https://c.example"}, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	active := newSite(t, s, "https://a.example", &owner)
	if err := s.AddOrigin(ctx, active.ID, "https://www.a.example"); err != nil {
		t.Fatal(err)
	}
	s.exec(t, `UPDATE sites SET created_at = now() - interval '8 days' WHERE id <> $1`, fresh.ID)
	s.exec(t, `UPDATE site_origins SET created_at = now() - interval '8 days' WHERE site_id <> $1`, fresh.ID)
	if n, err := s.CountSites(ctx, owner.ID); err != nil || n != 3 {
		t.Fatalf("sites = %d %v", n, err)
	}
	if _, err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SiteByID(ctx, squat.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("unverified site kept: %v", err)
	}
	if _, err := s.SiteByID(ctx, fresh.ID); err != nil {
		t.Errorf("fresh site removed: %v", err)
	}
	x, err := s.SiteByID(ctx, active.ID)
	if err != nil || len(x.Origins) != 1 || x.Status != model.SiteActive {
		t.Errorf("active site = %+v %v", x, err)
	}
}

// An export carries no text of comments their authors deleted.
func TestExportLeavesOutDeletedText(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ann := newUser(t, s, "ann", "Ann")
	site := newSite(t, s, "https://a.example", nil)
	c, err := s.CreateComment(ctx, NewComment{SiteID: site.ID, ThreadKey: "p:/x", ThreadURL: "https://a.example/x", AuthorKind: model.AuthorUser,
		AuthorUserID: &ann.ID, BodyMD: "regret", BodyHTML: "<p>regret</p>", RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOwnComment(ctx, c.ID, ann.ID); err != nil {
		t.Fatal(err)
	}
	_, comments, err := s.ExportSite(ctx, site.ID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("export = %+v %v", comments, err)
	}
	if comments[0].BodyMD != "" || comments[0].Status != model.StatusDeleted {
		t.Errorf("exported = %+v", comments[0])
	}
}
