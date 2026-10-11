// Package idptest fakes the two identity service reads behind every event
// (package idp): a user's current state, and the event search's removals.
package idptest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/kite-plus/comments/internal/idp"
)

// Server answers GET /v2/users/{sub} and POST /admin/v1/events/_search.
// Users are active unless set otherwise.
type Server struct {
	*httptest.Server
	mu      sync.Mutex
	states  map[string]string
	removed map[string]bool
}

// New starts a fake; Close it when done.
func New() *Server {
	f := &Server{states: map[string]string{}, removed: map[string]bool{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// Client reads this fake.
func (f *Server) Client() *idp.Client { return &idp.Client{BaseURL: f.URL, Token: "test"} }

// Set gives a user a ZITADEL state such as "USER_STATE_INACTIVE"; ""
// makes the user unknown, as after a removal.
func (f *Server) Set(sub, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[sub] = state
}

// Remove makes a user unknown and puts their removal in the event search.
func (f *Server) Remove(sub string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[sub], f.removed[sub] = "", true
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v2/users/"):
		sub := strings.TrimPrefix(r.URL.Path, "/v2/users/")
		state, ok := f.states[sub]
		if !ok {
			state = "USER_STATE_ACTIVE"
		}
		if state == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":5,"message":"User could not be found"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"state": state}})
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/events/_search":
		var q struct {
			AggregateID string `json:"aggregateId"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		events := []any{}
		if q.AggregateID != "" && f.removed[q.AggregateID] {
			events = append(events, map[string]any{"aggregate": map[string]any{"id": q.AggregateID}, "sequence": "1",
				"type": map[string]any{"type": idp.UserRemoved}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
	default:
		http.NotFound(w, r)
	}
}
