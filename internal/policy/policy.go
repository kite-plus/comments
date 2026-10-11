// Package policy holds the product's numbers: limits from
// docs/design/moderation.md section 3.4, retention from
// docs/design/data-model.md section 4 and the login parameters of
// docs/design/login.md. They are product rules, not configuration: change a
// value here together with the section that states it.
package policy

import "time"

// Content limits (moderation.md section 3.4).
const (
	CommentMaxRunes   = 5000
	LinksPerComment   = 5
	DisplayNameRunes  = 40
	HomepageMaxRunes  = 300
	ReportNoteRunes   = 500
	ThreadTitleRunes  = 300
	ThreadURLRunes    = 2000
	PageKeyMaxRunes   = 200
	ExcerptRunes      = 200 // plain text quoted in notification mail
	SiteNameRunes     = 100
	AliasOrigins      = 5
	BlockedWords      = 200
	BlockedWordRunes  = 100
	QueryKeys         = 5
	ImportMaxBytes    = 50 << 20
	EditWindow        = 24 * time.Hour
	NewAccountAge     = 24 * time.Hour
	TrustedAccountAge = 7 * 24 * time.Hour
	TrustedComments   = 3
	TrustedSites      = 2
	ReportThreshold   = 3
	RepliesInline     = 3
)

// Rate limits (moderation.md section 3.4). Each is a count per window.
var (
	NewThreadsPerUser   = Rate{5, time.Hour}
	NewThreadsPerGuest  = Rate{3, time.Hour}
	MemberPostsPerSite  = Rate{3, time.Minute}
	MemberPostsOverall  = Rate{20, 10 * time.Minute}
	GuestPostsPerIP     = Rate{5, 10 * time.Minute}
	ReportsPerUser      = Rate{30, 24 * time.Hour}
	ClaimMailsPerUser   = Rate{3, time.Hour}
	GuestConfirmsPerDay = Rate{1, 24 * time.Hour}
	VerifyPerSite       = Rate{10, time.Hour}
	LoginStartsPerIP    = Rate{10, time.Minute}
	RedeemsPerIP        = Rate{120, time.Minute}
	AnonymousReadsPerIP = Rate{300, time.Minute}
)

// Rate is a fixed-window limit.
type Rate struct {
	Max    int
	Window time.Duration
}

// Spam rules (moderation.md section 3.1).
const (
	DuplicateWindow   = 10 * time.Minute
	CrossSiteWindow   = time.Hour
	CrossSiteMaxSites = 3
)

// Login and sessions (login.md sections 4.5 and 5, data-model.md 3.2).
const (
	LoginTransactionTTL = 30 * time.Minute
	BinderTTL           = 24 * time.Hour
	SiteSessionTTL      = 90 * 24 * time.Hour
	SiteSessionMaxAge   = 400 * 24 * time.Hour
	ConsoleSessionTTL   = 30 * 24 * time.Hour
	// A session's expiry slides forward at most once in this period, so
	// using a session rarely writes to it.
	SessionTouchEvery = 24 * time.Hour
	// Popup handshake: the widget falls back to the redirect flow when the
	// popup has not said ready within this time.
	PopupReadyTimeout = 3 * time.Second
)

// Site verification (sites.md section 2).
const (
	VerificationTokenTTL = 7 * 24 * time.Hour
	VerifyTimeout        = 10 * time.Second
	VerifyMaxBytes       = 1 << 20
	VerifyMaxRedirects   = 3
	ReverifyEvery        = 30 * 24 * time.Hour
	ReverifyGrace        = 14 * 24 * time.Hour
	// Sites go through platform review before they turn active; open
	// registration in C3 turns this off (roadmap.md section 5).
	SiteApprovalRequired = true
)

// Retention (data-model.md section 4).
const (
	TransactionsKeptAfterExpiry = 24 * time.Hour
	DeletedBodyKept             = 14 * 24 * time.Hour
	HiddenBodyKept              = 90 * 24 * time.Hour
	RevisionsKept               = 30 * 24 * time.Hour
	IPHashKept                  = 30 * 24 * time.Hour
	NotificationsKept           = 180 * 24 * time.Hour
	SentMailKept                = 7 * 24 * time.Hour
	FailedMailKept              = 30 * 24 * time.Hour
	ResolvedReportsKept         = 180 * 24 * time.Hour
	ModerationEventsKept        = 365 * 24 * time.Hour
	UnconfirmedGuestEmailsKept  = 30 * 24 * time.Hour
	FinishedClaimsKept          = 30 * 24 * time.Hour
	ImportFilesKept             = 24 * time.Hour
	IDPEventsKept               = 30 * 24 * time.Hour
	DeletedSitesKept            = 30 * 24 * time.Hour
)

// Mail (moderation.md section 6).
const (
	MailCoalesceWindow = 10 * time.Minute
	MailMaxAttempts    = 8
	MailFirstRetry     = time.Minute
	// The daily digest goes out at this hour, UTC: 09:00 in China.
	DigestHourUTC   = 1
	ClaimLinkTTL    = time.Hour
	GuestConfirmTTL = 30 * 24 * time.Hour
)

// Lists (api.md section 1).
const (
	CommentsPage    = 20
	CommentsPageMax = 50
	ListPage        = 30
	ListPageMax     = 100
	// around= returns at most this many top-level comments.
	AroundMax = 200
)

// Proof of work for guests (moderation.md section 3.2), after ALTCHA: the
// client searches 0..max for the number the challenge hides. The range
// doubles for every PowStepPosts guest posts the address made in the last
// GuestPostsPerIP window, up to PowMaxNumber.
const (
	PowTTL        = 10 * time.Minute
	PowBaseNumber = 100_000
	PowMaxNumber  = 2_000_000
	PowStepPosts  = 2
)
