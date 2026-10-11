// Package model holds the domain types shared by the store, the HTTP API and
// the worker. docs/design/data-model.md describes the tables behind them.
package model

import (
	"slices"
	"time"
)

// Site statuses (data-model.md section 3.3).
const (
	SiteUnverified    = "unverified"
	SitePendingReview = "pending_review"
	SiteActive        = "active"
	SiteSuspended     = "suspended"
	SiteDeleted       = "deleted"
)

// Comment statuses (moderation.md section 2).
const (
	StatusPending   = "pending"
	StatusPublished = "published"
	StatusSpam      = "spam"
	StatusHidden    = "hidden"
	StatusDeleted   = "deleted"
)

// Author kinds (data-model.md section 3.4).
const (
	AuthorUser       = "user"
	AuthorGuest      = "guest"
	AuthorExternal   = "external"
	AuthorFormerUser = "former_user"
)

// Site roles.
const (
	RoleOwner     = "owner"
	RoleModerator = "moderator"
)

// Policies.
const (
	GuestOff      = "off"
	GuestReview   = "review"
	GuestPublish  = "publish"
	MemberPublish = "publish"
	MemberReview  = "review"
	SortOldest    = "oldest"
	SortNewest    = "newest"
	NotifyInstant = "instant"
	NotifyDaily   = "daily"
	NotifyOff     = "off"
)

// Locales the interface and mail come in.
const (
	LocaleZH = "zh-CN"
	LocaleEN = "en"
)

// Site is a registered blog: a set of origins with settings.
type Site struct {
	ID             int64
	PublicID       string
	Name           string
	Status         string
	StatusNote     string
	GuestPolicy    string
	MemberPolicy   string
	SortOrder      string
	CloseAfterDays *int
	QueryKeys      []string
	AllowLocalhost bool
	OwnerNotify    string
	BlockedWords   []string
	CreatedBy      *int64
	VerifiedAt     *time.Time
	ApprovedAt     *time.Time
	DeletedAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Origins        []Origin
}

// Origin is one of a site's addresses.
type Origin struct {
	Origin        string
	Primary       bool
	VerifiedAt    *time.Time
	LastCheckedAt *time.Time
	FailingSince  *time.Time
	CreatedAt     time.Time
}

// VerifiedOrigins lists the origins that may embed the widget.
func (s Site) VerifiedOrigins() []string {
	var out []string
	for _, o := range s.Origins {
		if o.VerifiedAt != nil {
			out = append(out, o.Origin)
		}
	}
	return out
}

// HasVerifiedOrigin reports whether origin belongs to the site and is
// verified.
func (s Site) HasVerifiedOrigin(origin string) bool {
	return slices.Contains(s.VerifiedOrigins(), origin)
}

// PrimaryOrigin is the origin the site was registered with.
func (s Site) PrimaryOrigin() string {
	for _, o := range s.Origins {
		if o.Primary {
			return o.Origin
		}
	}
	if len(s.Origins) > 0 {
		return s.Origins[0].Origin
	}
	return ""
}

// User is a reader's account in this service.
type User struct {
	ID              int64
	PublicID        string
	DisplayName     string
	AvatarKind      string
	HomepageURL     string
	Locale          string
	EmailEnc        []byte
	EmailHMAC       []byte
	NotifyReplies   bool
	OnAccountDelete string
	IsPlatformAdmin bool
	SuspendedAt     *time.Time
	SuspendReason   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastLoginAt     *time.Time
	HasAvatar       bool
}

// Thread is one discussion page.
type Thread struct {
	ID              int64
	SiteID          int64
	PageKey         string
	URL             string
	Title           string
	PagePublishedAt *time.Time
	LockedAt        *time.Time
	CommentCount    int
	LastCommentAt   *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ClosesAt is when the thread stops taking comments by the site's
// automatic closing, or nil.
func (t Thread) ClosesAt(closeAfterDays *int) *time.Time {
	if closeAfterDays == nil {
		return nil
	}
	from := t.CreatedAt
	if t.PagePublishedAt != nil {
		from = *t.PagePublishedAt
	}
	at := from.Add(time.Duration(*closeAfterDays) * 24 * time.Hour)
	return &at
}

// Comment is a comment with its author resolved for display.
type Comment struct {
	ID              int64
	SiteID          int64
	ThreadID        int64
	RootID          *int64
	ParentID        *int64
	AuthorKind      string
	AuthorUserID    *int64
	AuthorName      string // resolved: the account's current name for users
	AuthorURL       string
	AuthorRef       string
	AuthorPublicID  string // the site author ID, for users only
	AuthorHasAvatar bool   // the account has an uploaded avatar
	AuthorAvatarTag string // changes when the avatar does
	AuthorEmailEnc  []byte
	AuthorEmailHMAC []byte
	NotifyEmail     bool
	BodyMD          string
	BodyHTML        string
	RenderVersion   int
	Status          string
	PinnedAt        *time.Time
	Source          string
	SourceID        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	EditedAt        *time.Time
	PublishedAt     *time.Time
	Badge           string // "owner" or "moderator" when the author manages the site
	ReplyTo         *ReplyTo
	ReplyCount      int
	Replies         []Comment
	// Set on list results of the reader center.
	SiteName    string
	SitePublic  string
	ThreadURL   string
	ThreadTitle string
}

// ReplyTo names the comment a reply answers, when it is not the top-level
// comment's author.
type ReplyTo struct {
	ID   int64
	Name string
}

// Viewer is a reader as the widget sees them on one site.
type Viewer struct {
	UserID        int64
	AuthorID      string
	DisplayName   string
	Role          string // "", "owner", "moderator"
	NotifyReplies bool
	HasAvatar     bool
	AvatarTag     string
	HasEmail      bool
	Suspended     bool
}

// Manages reports whether the viewer may moderate the site.
func (v Viewer) Manages() bool { return v.Role == RoleOwner || v.Role == RoleModerator }

// Cursor positions a page in a list ordered by time and id.
type Cursor struct {
	At time.Time
	ID int64
}
