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
	"net/url"
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

// Types lists the event types reconciliation asks for. Locking is left
// out: anyone can lock a reader by guessing wrong passwords, so it never
// suspends anyone here.
var Types = []string{UserRemoved, UserDeactivated, UserReactivated}

var (
	ErrSignature = errors.New("webhook signature is not valid")
	// ErrUnavailable means the identity service could not be read; the
	// event waits for the next reconciliation.
	ErrUnavailable = errors.New("identity service unavailable")
	// ErrUnconfirmed means the user looks deleted but the event search
	// has no removal for them yet.
	ErrUnconfirmed = errors.New("removal not confirmed by the event search")
)

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
	// FromSearch marks an event read from the event search, which is
	// authoritative; webhook deliveries are only hints.
	FromSearch bool
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

// SearchOverlap is how far before the stored cursor a reconciliation run
// starts, against events committed slightly out of order; repeats
// deduplicate by ID.
const SearchOverlap = 5 * time.Minute

// Search returns up to limit user events created after from (exclusive;
// zero for the beginning), oldest first, and the creation time of the last
// one, from where the next page goes on.
func (c *Client) Search(ctx context.Context, from time.Time, limit int) ([]Event, time.Time, error) {
	req := map[string]any{"asc": true, "limit": limit, "eventTypes": Types, "aggregateTypes": []string{"user"}}
	if !from.IsZero() {
		req["from"] = from.UTC().Format(time.RFC3339Nano)
	}
	cursor := from
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/admin/v1/events/_search", bytes.NewReader(body))
	if err != nil {
		return nil, cursor, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.do(r)
	if err != nil {
		return nil, cursor, fmt.Errorf("%w: %w", ErrUnavailable, err)
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
		events = append(events, Event{ID: e.Aggregate.ID + ":" + seq, Type: e.Type.Type, Subject: e.Aggregate.ID, At: e.CreationDate, FromSearch: true})
		if !e.CreationDate.IsZero() {
			next = e.CreationDate
		}
	}
	return events, next, nil
}

func (c *Client) do(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+c.Token)
	r.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return hc.Do(r)
}

// User states as the identity service reports them.
const (
	StateActive   = "active"
	StateInactive = "inactive"
	StateLocked   = "locked"
	StateDeleted  = "deleted"
)

// UserState reads a user's current state: deleted, inactive, locked or
// active. A 404 counts as deleted only when its body is the identity
// service's own not-found error (gRPC code 5), not a proxy's page.
func (c *Client) UserState(ctx context.Context, sub string) (string, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v2/users/"+url.PathEscape(sub), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(r)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		var e struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Code == 5 {
			return StateDeleted, nil
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: user lookup: HTTP %d: %.200s", ErrUnavailable, resp.StatusCode, raw)
	}
	var out struct {
		User struct {
			State string `json:"state"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	switch out.User.State {
	case "USER_STATE_INACTIVE":
		return StateInactive, nil
	case "USER_STATE_LOCKED":
		return StateLocked, nil
	case "USER_STATE_DELETED":
		return StateDeleted, nil
	}
	return StateActive, nil
}

// Removed reports whether the event search, which is authoritative, has a
// removal of the user.
func (c *Client) Removed(ctx context.Context, sub string) (bool, error) {
	body, _ := json.Marshal(map[string]any{"asc": true, "limit": 1, "eventTypes": []string{UserRemoved},
		"aggregateTypes": []string{"user"}, "aggregateId": sub})
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/admin/v1/events/_search", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.do(r)
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: event search: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false, err
	}
	return len(out.Events) > 0, nil
}

// stateOf is what an event implies when there is no client to ask.
func stateOf(eventType string) string {
	switch eventType {
	case UserRemoved:
		return StateDeleted
	case UserDeactivated:
		return StateInactive
	}
	return StateActive
}

// Apply acts on one event once. With a client, a webhook delivery is only
// a hint, since another service's token can read this service's signing
// key: the user's current state is read from the identity service and
// applied, and data is deleted only when the user is gone and the
// authoritative event search has their removal. Without a client (tests),
// the event type decides. An inactive account is suspended and an active
// one has the identity service's suspension lifted; locking changes
// nothing. A deleted account's data goes as its on_account_delete setting
// says. Events for readers who never used this service are recorded and
// otherwise ignored. ErrUnavailable and ErrUnconfirmed leave the event
// unrecorded for the next reconciliation. Only search events are recorded
// in idp_events: a push is a hint anyone holding a signing key could send,
// so it must never mark an event handled; acting on the current state is
// safe to repeat.
func Apply(ctx context.Context, st *store.Store, c *Client, issuer string, ev Event) (applied bool, err error) {
	if ev.FromSearch {
		seen, err := st.IDPEventSeen(ctx, ev.ID)
		if err != nil || seen {
			return false, err
		}
	}
	u, err := st.UserBySubject(ctx, issuer, ev.Subject)
	switch {
	case errors.Is(err, store.ErrNotFound) || ev.Type == UserLocked || ev.Type == UserUnlocked:
	case err != nil:
		return false, err
	default:
		state := stateOf(ev.Type)
		if c != nil {
			if state, err = c.UserState(ctx, ev.Subject); err != nil {
				return false, err
			}
			// A removal read from the search is its own confirmation.
			confirmed := ev.FromSearch && ev.Type == UserRemoved
			if state == StateDeleted && !confirmed {
				removed, err := c.Removed(ctx, ev.Subject)
				if err != nil {
					return false, err
				}
				if !removed {
					return false, ErrUnconfirmed
				}
			}
		}
		switch state {
		case StateDeleted:
			err = st.DeleteUserData(ctx, u.ID, u.OnAccountDelete, true)
		case StateInactive:
			err = st.SetIDPSuspended(ctx, u.ID, true)
		case StateLocked:
		default:
			err = st.SetIDPSuspended(ctx, u.ID, false)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return false, err
		}
	}
	if !ev.FromSearch {
		return true, nil
	}
	return true, st.RecordIDPEvent(ctx, ev.ID, ev.Type)
}
