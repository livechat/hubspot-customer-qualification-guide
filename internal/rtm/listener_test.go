package rtm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/livechat/hubspot-customer-qualification-example/internal/hubspot"
	"github.com/livechat/hubspot-customer-qualification-example/internal/qualification"
	"github.com/livechat/hubspot-customer-qualification-example/internal/safehttp"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

func TestWebSocketEntryExitHeartbeatAndReconnectWithoutBackfill(t *testing.T) {
	var member atomic.Bool

	var currentMu sync.Mutex

	current := ""
	writes := make(chan string, 8)
	connections := make(chan *websocket.Conn, 4)

	var pings atomic.Int32

	var getCalls atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("organization_id") != "synthetic-org" {
			t.Error("missing organization")

			return
		}

		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}

		defer conn.CloseNow()

		var login struct {
			RequestID string `json:"request_id"`
			Action    string
			Payload   struct {
				Token      string
				Away       bool
				Monitoring string `json:"customer_monitoring_level"`
				Pushes     map[string][]string
			}
		}

		if wsjson.Read(ctx, conn, &login) != nil {
			return
		}

		if login.Action != "login" || login.Payload.Token != "Basic synthetic-auth" ||
			!login.Payload.Away || login.Payload.Monitoring != "online" {
			t.Error("wrong login or monitoring settings")

			return
		}

		pushes := login.Payload.Pushes["3.6"]
		if len(pushes) != 1 || pushes[0] != "segment_memberships_updated" {
			t.Error("login must request only segment membership pushes")

			return
		}

		if err := wsjson.Write(ctx, conn, map[string]any{
			"request_id": login.RequestID,
			"type":       "response",
			"action":     "login",
			"success":    true,
			"payload":    map[string]any{},
		}); err != nil {
			return
		}

		connections <- conn

		for {
			var req struct {
				RequestID string `json:"request_id"`
				Action    string
			}

			if wsjson.Read(ctx, conn, &req) != nil {
				return
			}

			if req.Action != "ping" {
				t.Errorf("unexpected request after login: %s", req.Action)

				return
			}

			pings.Add(1)
			wsjson.Write(
				ctx,
				conn,
				map[string]any{
					"request_id": req.RequestID,
					"action":     "ping",
					"type":       "response",
					"success":    true,
				},
			)
		}
	})
	mux.HandleFunc("/get_merged_customer", func(w http.ResponseWriter, r *http.Request) {
		getCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{
			"customer_id": "synthetic-customer",
			"cluster_id":  "synthetic-customer",
			"monitoring": map[string]string{
				"email": "person@example.com",
			},
			"segment_memberships": []any{
				map[string]any{
					"segment_id": 1,
					"is_member":  member.Load(),
				},
			},
		})
	})
	mux.HandleFunc("/v1/list_customers", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"customers": []any{
				map[string]any{
					"customer_id": "synthetic-customer",
					"cluster_id":  "synthetic-customer",
					"monitoring": map[string]string{
						"email": "person@example.com",
					},
				},
			},
		})
	})
	mux.HandleFunc("/crm/objects/2026-09/contacts/", func(w http.ResponseWriter, r *http.Request) {
		currentMu.Lock()
		defer currentMu.Unlock()

		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{
				"id": "hubspot-person",
				"properties": map[string]string{
					"email":               "person@example.com",
					"text_qualified_lead": current,
				},
			})

			return
		}

		var input struct {
			Properties map[string]string
		}
		json.NewDecoder(r.Body).Decode(&input)

		if len(input.Properties) != 1 {
			t.Error("updated unrelated property")
		}

		current = input.Properties[hubspot.Property]
		writes <- current

		w.Write([]byte(`{}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	hs := &hubspot.Client{
		API: safehttp.New(srv.URL, "Bearer synthetic"),
	}
	hs.API.Interval = 0
	text := &textapi.Client{
		API: safehttp.New(srv.URL, "Basic synthetic"),
	}
	text.API.Interval = 0
	l := &Listener{
		URL:          "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws",
		Organization: "synthetic-org",
		Auth:         "synthetic-auth",
		Segment:      1,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Syncer: &qualification.Service{
			Text:    text,
			HubSpot: hs,
			Segment: 1,
		},
		PingInterval:   20 * time.Millisecond,
		ReadTimeout:    time.Second,
		ReconnectDelay: time.Millisecond,
	}

	done := make(chan error, 1)
	go func() {
		done <- l.Run(ctx)
	}()

	defer func() {
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("listener did not shut down")
		}
	}()

	nextConn := func() *websocket.Conn {
		select {
		case c := <-connections:
			return c
		case <-time.After(3 * time.Second):
			t.Fatal("no RTM connection")

			return nil
		}
	}
	conn := nextConn()

	if getCalls.Load() != 0 {
		t.Fatal("initial login caused backfill")
	}

	push := func(c *websocket.Conn, on bool) {
		member.Store(on)

		key := "segments_added"

		if !on {
			key = "segments_removed"
		}

		payload := map[string]any{
			"customer_id": "synthetic-customer",
			"segment_memberships": []any{
				map[string]any{
					"segment_id": 1,
					"is_member":  on,
				},
			},
			key: []int{1},
		}

		if err := wsjson.Write(
			ctx,
			c,
			map[string]any{
				"type":    "push",
				"action":  "segment_memberships_updated",
				"payload": payload,
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	expectWrite := func(wanted string) {
		select {
		case got := <-writes:
			if got != wanted {
				t.Fatalf("got %s want %s", got, wanted)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no HubSpot update")
		}
	}

	push(conn, true)
	expectWrite("true")
	push(conn, false)
	expectWrite("false")

	before := getCalls.Load()

	conn.CloseNow()
	member.Store(true) // Change while disconnected is deliberately not backfilled.
	nextConn()
	time.Sleep(100 * time.Millisecond)

	if getCalls.Load() != before || l.Updated.Load() != 2 || pings.Load() == 0 {
		t.Fatalf(
			"gets before=%d after=%d updates=%d pings=%d",
			before,
			getCalls.Load(),
			l.Updated.Load(),
			pings.Load(),
		)
	}
}
