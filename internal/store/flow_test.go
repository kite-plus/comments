package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kite-plus/comments/internal/model"
)

func hash(s string) []byte {
	b := make([]byte, 32)
	copy(b, s)
	return b
}

func newUser(t *testing.T, s *Store, sub, name string) model.User {
	t.Helper()
	u, created, err := s.LoginUser(context.Background(), OIDCLogin{Issuer: "https://id.example", Subject: sub, DisplayName: name,
		Locale: "zh-CN", EmailEnc: []byte("enc-" + sub), EmailHMAC: hash("mail-" + sub)})
	if err != nil || !created {
		t.Fatalf("login %s: %v", sub, err)
	}
	return u
}

func newSite(t *testing.T, s *Store, origin string, owner *model.User) model.Site {
	t.Helper()
	n := NewSite{Name: origin, Origins: []string{origin}, Verified: true}
	if owner != nil {
		n.CreatedBy = &owner.ID
	}
	site, err := s.CreateSite(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func TestWidgetLoginTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newUser(t, s, "sub-1", "Ann")
	site := newSite(t, s, "https://a.example", nil)
	other := newSite(t, s, "https://b.example", nil)

	id, err := s.CreateLoginTx(ctx, LoginTx{Mode: ModePopup, SiteID: &site.ID, Challenge: hash("c1"), StateHash: hash("s1"),
		BinderHash: hash("b1"), Nonce: "n", PKCEVerifier: "v", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLoginTx(ctx, LoginTx{Mode: ModePopup, SiteID: &site.ID, Challenge: hash("c1"), StateHash: hash("s2"),
		BinderHash: hash("b1"), Nonce: "n", PKCEVerifier: "v", ExpiresAt: time.Now().Add(time.Hour)}); !errors.Is(err, ErrConflict) {
		t.Errorf("reused challenge: %v", err)
	}
	in := SiteSessionIn{TokenHash: hash("tok"), ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := s.RedeemWidgetLogin(ctx, site.ID, hash("c1"), in); !errors.Is(err, ErrLoginPending) {
		t.Errorf("redeem before callback: %v", err)
	}
	if _, err := s.RedeemWidgetLogin(ctx, site.ID, hash("unknown"), in); !errors.Is(err, ErrLoginPending) {
		t.Errorf("unknown challenge: %v", err)
	}
	if err := s.AuthenticateLoginTx(ctx, id, u.ID, "sid-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AuthenticateLoginTx(ctx, id, u.ID, "sid-1"); !errors.Is(err, ErrLoginGone) {
		t.Errorf("second callback: %v", err)
	}
	if _, err := s.RedeemWidgetLogin(ctx, other.ID, hash("c1"), in); !errors.Is(err, ErrLoginPending) {
		t.Errorf("redeemed on another site: %v", err)
	}
	v, err := s.RedeemWidgetLogin(ctx, site.ID, hash("c1"), in)
	if err != nil || v.UserID != u.ID || v.AuthorID == "" {
		t.Fatalf("redeem = %+v, %v", v, err)
	}
	if _, err := s.RedeemWidgetLogin(ctx, site.ID, hash("c1"), SiteSessionIn{TokenHash: hash("tok2"), ExpiresAt: time.Now().Add(time.Hour)}); !errors.Is(err, ErrLoginGone) {
		t.Errorf("second redeem: %v", err)
	}
	got, _, err := s.SiteViewer(ctx, hash("tok"), site.ID)
	if err != nil || got.AuthorID != v.AuthorID {
		t.Fatalf("viewer = %+v, %v", got, err)
	}
	if _, _, err := s.SiteViewer(ctx, hash("tok"), other.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("session used on another site: %v", err)
	}

	// Back-channel logout ends what the identity session issued.
	if n, err := s.DeleteSessionsBySID(ctx, "sid-1"); err != nil || n != 1 {
		t.Errorf("logout by sid = %d, %v", n, err)
	}
	if _, _, err := s.SiteViewer(ctx, hash("tok"), site.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("session survived logout: %v", err)
	}
}

func TestRedirectConfirmNeedsBinder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := newUser(t, s, "sub-1", "Ann")
	site := newSite(t, s, "https://a.example", nil)
	id, err := s.CreateLoginTx(ctx, LoginTx{Mode: ModeRedirect, SiteID: &site.ID, Challenge: hash("c"), ReturnTo: "https://a.example/p",
		StateHash: hash("s"), BinderHash: hash("b"), Nonce: "n", PKCEVerifier: "v", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConfirmLoginTx(ctx, id, hash("b")); !errors.Is(err, ErrInvalid) {
		t.Errorf("confirm before callback: %v", err)
	}
	if err := s.AuthenticateLoginTx(ctx, id, u.ID, "sid"); err != nil {
		t.Fatal(err)
	}
	in := SiteSessionIn{TokenHash: hash("t"), ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := s.RedeemWidgetLogin(ctx, site.ID, hash("c"), in); !errors.Is(err, ErrLoginPending) {
		t.Errorf("redeemed before confirmation: %v", err)
	}
	if _, err := s.ConfirmLoginTx(ctx, id, hash("other")); !errors.Is(err, ErrInvalid) {
		t.Errorf("confirm from another browser: %v", err)
	}
	tx, err := s.ConfirmLoginTx(ctx, id, hash("b"))
	if err != nil || tx.ReturnTo != "https://a.example/p" {
		t.Fatalf("confirm = %+v, %v", tx, err)
	}
	if _, err := s.RedeemWidgetLogin(ctx, site.ID, hash("c"), in); err != nil {
		t.Errorf("redeem after confirmation: %v", err)
	}
}

func TestCommentsAndNotifications(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	ann := newUser(t, s, "ann", "Ann")
	bob := newUser(t, s, "bob", "Bob")
	site := newSite(t, s, "https://a.example", &owner)

	post := func(u model.User, parent *int64, body, status string) model.Comment {
		t.Helper()
		c, err := s.CreateComment(ctx, NewComment{SiteID: site.ID, ThreadKey: "p:/x", ThreadURL: "https://a.example/x", ThreadTitle: "X",
			ParentID: parent, AuthorKind: model.AuthorUser, AuthorUserID: &u.ID, BodyMD: body, BodyHTML: "<p>" + body + "</p>",
			RenderVersion: 1, Status: status})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	top := post(ann, nil, "hello", model.StatusPublished)
	if top.AuthorPublicID == "" || top.AuthorName != "Ann" {
		t.Errorf("author not resolved: %+v", top)
	}
	reply := post(bob, &top.ID, "hi ann", model.StatusPublished)
	if reply.RootID == nil || *reply.RootID != top.ID {
		t.Errorf("reply root = %v", reply.RootID)
	}
	deep := post(ann, &reply.ID, "hi bob", model.StatusPublished)
	if *deep.RootID != top.ID || *deep.ParentID != reply.ID || deep.ReplyTo == nil || deep.ReplyTo.Name != "Bob" {
		t.Errorf("deep reply = %+v", deep)
	}
	pending := post(bob, &top.ID, "later", model.StatusPending)

	// Ann got one notification (Bob's reply), Bob one (Ann's), none for
	// the pending reply until it is published.
	list, _, unread, err := s.Notifications(ctx, ann.ID, nil, 10)
	if err != nil || len(list) != 1 || unread != 1 || list[0].Comment.ID != reply.ID {
		t.Fatalf("ann's notifications = %+v, %d, %v", list, unread, err)
	}
	var queued int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE kind = 'reply' AND status = 'queued'`).Scan(&queued); err != nil || queued != 2 {
		t.Errorf("queued reply mail = %d, %v", queued, err)
	}
	if _, err := s.Moderate(ctx, ModerateIn{SiteID: site.ID, CommentID: pending.ID, Action: "approve", Actor: &owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, unread, _ = s.Notifications(ctx, ann.ID, nil, 10); unread != 2 {
		t.Errorf("unread after approval = %d", unread)
	}
	// Hide and restore do not notify again.
	if _, err := s.Moderate(ctx, ModerateIn{SiteID: site.ID, CommentID: pending.ID, Action: "hide", Actor: &owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Moderate(ctx, ModerateIn{SiteID: site.ID, CommentID: pending.ID, Action: "restore", Actor: &owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, unread, _ = s.Notifications(ctx, ann.ID, nil, 10); unread != 2 {
		t.Errorf("unread after restore = %d", unread)
	}
	if _, err := s.Moderate(ctx, ModerateIn{SiteID: site.ID, CommentID: reply.ID, Action: "pin"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("pinned a reply: %v", err)
	}

	thread, err := s.ThreadByKey(ctx, site.ID, "p:/x")
	if err != nil || thread.CommentCount != 4 {
		t.Fatalf("thread = %+v, %v", thread, err)
	}
	page, err := s.ListComments(ctx, ListQuery{ThreadID: thread.ID, Limit: 20})
	if err != nil || len(page.Comments) != 1 || page.Comments[0].ReplyCount != 3 || len(page.Comments[0].Replies) != 3 {
		t.Fatalf("page = %+v, %v", page, err)
	}

	// The author deletes the top-level comment: it stays as a placeholder
	// while replies are visible.
	if err := s.DeleteOwnComment(ctx, top.ID, ann.ID); err != nil {
		t.Fatal(err)
	}
	page, _ = s.ListComments(ctx, ListQuery{ThreadID: thread.ID, Limit: 20})
	if len(page.Comments) != 1 || page.Comments[0].Status != model.StatusDeleted {
		t.Errorf("placeholder missing: %+v", page.Comments)
	}
	if err := s.DeleteOwnComment(ctx, top.ID, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted someone else's comment: %v", err)
	}
}

func TestMailCoalesces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for range 3 {
		if err := s.EnqueueMail(ctx, Mail{Kind: "reply", ToEnc: []byte("x"), CoalesceKey: "reply:u:1", Locale: "en", Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := s.ClaimMail(ctx, 1, time.Minute)
	if err != nil || len(batch) != 3 {
		t.Fatalf("claimed %d, %v", len(batch), err)
	}
	ids := []int64{batch[0].ID, batch[1].ID, batch[2].ID}
	if err := s.MailSent(ctx, ids); err != nil {
		t.Fatal(err)
	}
	// The next mail with the key waits out the coalesce window.
	if err := s.EnqueueMail(ctx, Mail{Kind: "reply", ToEnc: []byte("x"), CoalesceKey: "reply:u:1", Locale: "en", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if batch, _ := s.ClaimMail(ctx, 10, time.Minute); len(batch) != 0 {
		t.Errorf("mail inside the coalesce window was due: %d", len(batch))
	}
	if err := s.EnqueueMail(ctx, Mail{Kind: "claim", ToEnc: []byte("y"), Locale: "en", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	batch, _ = s.ClaimMail(ctx, 10, time.Minute)
	if len(batch) != 1 || batch[0].Kind != "claim" {
		t.Fatalf("uncoalesced mail = %+v", batch)
	}
	if err := s.MailFailed(ctx, []int64{batch[0].ID}, "boom"); err != nil {
		t.Fatal(err)
	}
	if batch, _ := s.ClaimMail(ctx, 10, time.Minute); len(batch) != 0 {
		t.Error("failed mail retried at once")
	}
}

func TestDeleteUserData(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	ann := newUser(t, s, "ann", "Ann")
	bob := newUser(t, s, "bob", "Bob")
	site := newSite(t, s, "https://a.example", &owner)
	mk := func(u model.User, parent *int64) model.Comment {
		c, err := s.CreateComment(ctx, NewComment{SiteID: site.ID, ThreadKey: "p:/x", ThreadURL: "https://a.example/x",
			ParentID: parent, AuthorKind: model.AuthorUser, AuthorUserID: &u.ID, BodyMD: "b", BodyHTML: "<p>b</p>", RenderVersion: 1,
			Status: model.StatusPublished})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a := mk(ann, nil)
	mk(bob, &a.ID)
	b := mk(bob, nil)

	if err := s.DeleteUserData(ctx, owner.ID, "remove", false); !errors.Is(err, ErrSoleOwner) {
		t.Errorf("sole owner deleted: %v", err)
	}
	if err := s.DeleteUserData(ctx, ann.ID, "remove", false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Comment(ctx, a.ID)
	if got.Status != model.StatusDeleted || got.AuthorKind != model.AuthorFormerUser || got.AuthorUserID != nil {
		t.Errorf("removed comment = %+v", got)
	}
	if err := s.DeleteUserData(ctx, bob.ID, "anonymize", false); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Comment(ctx, b.ID)
	if got.Status != model.StatusPublished || got.AuthorKind != model.AuthorFormerUser || got.AuthorName != "" {
		t.Errorf("anonymized comment = %+v", got)
	}
	if _, err := s.User(ctx, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("user row survived: %v", err)
	}
	// From the identity service, the sole owner's site enters deletion.
	if err := s.DeleteUserData(ctx, owner.ID, "remove", true); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteDeleted {
		t.Errorf("orphaned site status = %s", x.Status)
	}
	thread, _ := s.ThreadByKey(ctx, site.ID, "p:/x")
	if thread.CommentCount != 2 {
		t.Errorf("count after deletions = %d", thread.CommentCount)
	}
}

func TestSiteLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := newUser(t, s, "owner", "Owner")
	site, err := s.CreateSite(ctx, NewSite{Name: "A", Origins: []string{"https://a.example", "https://www.a.example"}, CreatedBy: &owner.ID})
	if err != nil || site.Status != model.SiteUnverified || len(site.Origins) != 2 || site.PrimaryOrigin() != "https://a.example" {
		t.Fatalf("site = %+v, %v", site, err)
	}
	// An unverified claim blocks nobody; the first to verify holds the origin.
	squat, err := s.CreateSite(ctx, NewSite{Name: "B", Origins: []string{"https://www.a.example"}, CreatedBy: &owner.ID})
	if err != nil {
		t.Fatalf("unverified origin blocked a second site: %v", err)
	}
	if err := s.MarkOriginVerified(ctx, site.ID, "https://www.a.example", hash("t")); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOriginVerified(ctx, squat.ID, "https://www.a.example", hash("u")); !errors.Is(err, ErrConflict) {
		t.Errorf("origin verified twice: %v", err)
	}
	if _, err := s.CreateSite(ctx, NewSite{Name: "C", Origins: []string{"https://www.a.example"}, CreatedBy: &owner.ID}); !errors.Is(err, ErrConflict) {
		t.Errorf("site created on a verified origin: %v", err)
	}
	if err := s.AddOrigin(ctx, squat.ID, "https://www.a.example"); !errors.Is(err, ErrConflict) {
		t.Errorf("verified origin added again: %v", err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SitePendingReview || !x.HasVerifiedOrigin("https://www.a.example") {
		t.Errorf("after verification = %+v", x)
	}
	if err := s.ApproveSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteActive {
		t.Errorf("after approval = %s", x.Status)
	}
	if err := s.RemoveOrigin(ctx, site.ID, "https://a.example"); !errors.Is(err, ErrNotFound) {
		t.Errorf("removed the primary origin: %v", err)
	}
	if err := s.RemoveOrigin(ctx, site.ID, "https://www.a.example"); err != nil {
		t.Fatal(err)
	}
	if x, _ := s.SiteByID(ctx, site.ID); x.Status != model.SiteUnverified {
		t.Errorf("without verified origins = %s", x.Status)
	}
	if err := s.DeleteSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreSite(ctx, site.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupRuns(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.exec(t, `SELECT 1`)
}

func TestReportThresholdNeedsTrustedReporters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	author := newUser(t, s, "author", "Author")
	site := newSite(t, s, "https://a.example", nil)
	c, err := s.CreateComment(ctx, NewComment{SiteID: site.ID, ThreadKey: "p:/x", ThreadURL: "https://a.example/x",
		AuthorKind: model.AuthorUser, AuthorUserID: &author.ID, BodyMD: "b", BodyHTML: "<p>b</p>", RenderVersion: 1, Status: model.StatusPublished})
	if err != nil {
		t.Fatal(err)
	}
	// Three reporters, made trusted: old enough, with published comments
	// on two sites they do not manage.
	other := newSite(t, s, "https://b.example", nil)
	var reporters []model.User
	for _, sub := range []string{"r1", "r2", "r3"} {
		u := newUser(t, s, sub, sub)
		for i, st := range []model.Site{site, other, other} {
			if _, err := s.CreateComment(ctx, NewComment{SiteID: st.ID, ThreadKey: "p:/t" + sub + string(rune('a'+i)), ThreadURL: "https://a.example/t",
				AuthorKind: model.AuthorUser, AuthorUserID: &u.ID, BodyMD: "x" + sub + string(rune('a'+i)), BodyHTML: "<p>x</p>", RenderVersion: 1,
				Status: model.StatusPublished}); err != nil {
				t.Fatal(err)
			}
		}
		reporters = append(reporters, u)
	}
	s.exec(t, `UPDATE users SET created_at = now() - interval '30 days' WHERE display_name IN ('r1', 'r2', 'r3')`)
	untrusted := newUser(t, s, "new", "New")
	if ok, err := s.Trusted(ctx, untrusted.ID); err != nil || ok {
		t.Fatalf("a new account counted as trusted: %v %v", ok, err)
	}
	for i, r := range reporters {
		trusted, err := s.Trusted(ctx, r.ID)
		if err != nil || !trusted {
			t.Fatalf("reporter %d not trusted: %v", i, err)
		}
		res, err := s.CreateReport(ctx, ReportIn{SiteID: site.ID, CommentID: c.ID, ReporterID: r.ID, Reason: "abuse", ReporterTrusted: trusted})
		if err != nil {
			t.Fatal(err)
		}
		if res.Returned != (i == 2) {
			t.Errorf("after report %d returned = %v", i+1, res.Returned)
		}
	}
	got, _ := s.Comment(ctx, c.ID)
	if got.Status != model.StatusPending {
		t.Errorf("status after three trusted reports = %s", got.Status)
	}
}
