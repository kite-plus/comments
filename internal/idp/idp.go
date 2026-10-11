// Package idp handles the identity service's user events
// (docs/design/login.md section 7): ZITADEL Actions v2 webhook deliveries,
// the hourly reconciliation through its event search API, and applying an
// event to this service's accounts exactly once.
package idp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kite-plus/comments/internal/store"
)

// SignatureHeader carries the webhook signature.
const SignatureHeader = "ZITADEL-Signature"

// SignatureTolerance bounds the age of a signed delivery, against replay.
const SignatureTolerance = 5 * time.Minute

// Event types this service acts on.
const (
	UserRemoved     = "user.removed"
	UserDeactivated = "user.deactivated"
	UserReactivated = "user.reactivated"
	UserLocked      = "user.locked"
	UserUnlocked    = "user.unlocked"
)

// Types lists the event types reconciliation asks for.
var Types = []string{UserRemoved, UserDeactivated, UserReactivated, UserLocked, UserUnlocked}

var ErrSignature = errors.New("webhook signature is not valid")

// VerifySignature checks a "t=<unix>,v1=<hex>" header: HMAC-SHA256 under
// the signing key over "<t>.<body>", within SignatureTolerance of now.
func VerifySignature(header string, body []byte, secret string, now time.Time) error {
	if secret == "" {
		return ErrSignature
	}
	var ts int64
	var sigs [][]byte
	for part := range strings.SplitSeq(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts, _ = strconv.ParseInt(v, 10, 64)
		case "v1":
			if b, err := hex.DecodeString(v); err == nil {
				sigs = append(sigs, b)
			}
		}
	}
	if ts == 0 || len(sigs) == 0 || now.Sub(time.Unix(ts, 0)).Abs() > SignatureTolerance {
		return ErrSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		if hmac.Equal(s, want) {
			return nil
		}
	}
	return ErrSignature
}

// Sign makes a signature header, for tests and the end-to-end harness.
func Sign(body []byte, secret string, now time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", now.Unix())
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

// Event is a user event, from either source.
type Event struct {
	// ID is "<aggregate id>:<sequence>", the same for a delivery and the
	// search result of one event, which makes them deduplicate.
	ID      string
	Type    string
	Subject string
	At      time.Time
}

// ParseWebhook reads an Actions v2 event delivery.
func ParseWebhook(body []byte) (Event, error) {
	var p struct {
		AggregateID   string          `json:"aggregateID"`
		AggregateType string          `json:"aggregateType"`
		Sequence      json.RawMessage `json:"sequence"`
		EventType     string          `json:"event_type"`
		CreatedAt     time.Time       `json:"created_at"`
		UserID        string          `json:"userID"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Event{}, fmt.Errorf("event payload: %w", err)
	}
	seq := strings.Trim(string(p.Sequence), `"`)
	if p.AggregateID == "" || p.EventType == "" || seq == "" {
		return Event{}, errors.New("event payload lacks aggregateID, event_type or sequence")
	}
	return Event{ID: p.AggregateID + ":" + seq, Type: p.EventType, Subject: p.AggregateID, At: p.CreatedAt}, nil
}

// Client searches the identity service's events with an API token.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Search returns user events from cursor on, oldest first, and the cursor
// to continue from. The cursor is the creation time of the last event seen;
// events at that instant come again and deduplicate.
func (c *Client) Search(ctx context.Context, cursor string, limit int) ([]Event, string, error) {
	req := map[string]any{"asc": true, "limit": limit, "event_types": Types, "aggregate_types": []string{"user"}}
	if cursor != "" {
		req["from"] = cursor
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/admin/v1/events/_search", bytes.NewReader(body))
	if err != nil {
		return nil, cursor, err
	}
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(r)
	if err != nil {
		return nil, cursor, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, cursor, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, cursor, fmt.Errorf("event search: HTTP %d: %.200s", resp.StatusCode, raw)
	}
	var out struct {
		Events []struct {
			Aggregate struct {
				ID string `json:"id"`
			} `json:"aggregate"`
			Sequence     json.RawMessage `json:"sequence"`
			CreationDate time.Time       `json:"creationDate"`
			Type         struct {
				Type string `json:"type"`
			} `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, cursor, fmt.Errorf("event search response: %w", err)
	}
	var events []Event
	next := cursor
	for _, e := range out.Events {
		seq := strings.Trim(string(e.Sequence), `"`)
		if e.Aggregate.ID == "" || e.Type.Type == "" {
			continue
		}
		events = append(events, Event{ID: e.Aggregate.ID + ":" + seq, Type: e.Type.Type, Subject: e.Aggregate.ID, At: e.CreationDate})
		if !e.CreationDate.IsZero() {
			next = e.CreationDate.UTC().Format(time.RFC3339Nano)
		}
	}
	return events, next, nil
}

// Apply acts on one event once: a removed account's data goes as its
// on_account_delete setting says; a deactivated or locked account is
// suspended; a reactivated or unlocked one is restored. Events for readers
// who never used this service are recorded and otherwise ignored.
func Apply(ctx context.Context, st *store.Store, issuer string, ev Event) (applied bool, err error) {
	seen, err := st.IDPEventSeen(ctx, ev.ID)
	if err != nil || seen {
		return false, err
	}
	u, err := st.UserBySubject(ctx, issuer, ev.Subject)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return false, err
	default:
		switch ev.Type {
		case UserRemoved:
			err = st.DeleteUserData(ctx, u.ID, u.OnAccountDelete, true)
		case UserDeactivated, UserLocked:
			err = st.SuspendUser(ctx, u.ID, "idp:"+strings.TrimPrefix(ev.Type, "user."))
		case UserReactivated, UserUnlocked:
			err = st.UnsuspendUser(ctx, u.ID, "idp:")
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
	}
	return true, st.RecordIDPEvent(ctx, ev.ID, ev.Type)
}
