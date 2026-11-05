package rtm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/livechat/hubspot-customer-qualification-example/internal/qualification"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

type Syncer interface {
	Sync(context.Context, string) (qualification.Outcome, error)
}

type Listener struct {
	URL          string
	Organization string
	Auth         string
	Segment      int
	Syncer       Syncer
	Logger       *slog.Logger
	Connected    atomic.Bool
	Received     atomic.Uint64
	Updated      atomic.Uint64
	Skipped      atomic.Uint64
	Failed       atomic.Uint64

	// Shorter durations are used only by local protocol tests.
	PingInterval   time.Duration
	ReadTimeout    time.Duration
	ReconnectDelay time.Duration
}

type frame struct {
	RequestID string          `json:"request_id"`
	Action    string          `json:"action"`
	Type      string          `json:"type"`
	Success   bool            `json:"success"`
	Payload   json.RawMessage `json:"payload"`
}

type push struct {
	CustomerID  string               `json:"customer_id"`
	Memberships []textapi.Membership `json:"segment_memberships"`
	Added       []int                `json:"segments_added"`
	Removed     []int                `json:"segments_removed"`
}

type sessionError struct {
	message string
	fatal   bool
}

func (e *sessionError) Error() string {
	return e.message
}

func (l *Listener) Run(ctx context.Context) error {
	ctx, cancelWorker := context.WithCancel(ctx)

	if l.PingInterval == 0 {
		l.PingInterval = 15 * time.Second
	}

	if l.ReadTimeout == 0 {
		l.ReadTimeout = 40 * time.Second
	}

	if l.ReconnectDelay == 0 {
		l.ReconnectDelay = time.Second
	}

	events := make(chan string, 256)
	workerDone := make(chan struct{})

	defer func() {
		cancelWorker()
		<-workerDone
	}()

	go func() {
		defer close(workerDone)

		for {
			select {
			case <-ctx.Done():
				return
			case id := <-events:
				job, cancel := context.WithTimeout(ctx, 90*time.Second)
				outcome, err := l.Syncer.Sync(job, id)

				cancel()

				if err != nil {
					l.Failed.Add(1)
					l.Logger.Error("qualification update failed", "error", err)

					continue
				}

				if outcome == qualification.Updated {
					l.Updated.Add(1)
				} else {
					l.Skipped.Add(1)
				}

				// Outcomes are named application values, never upstream response text.
				l.Logger.Info(
					"qualification event processed",
					"outcome", outcome,
					"updated", l.Updated.Load(),
					"skipped", l.Skipped.Load(),
					"failed", l.Failed.Load(),
				)
			}
		}
	}()

	// Cancel the worker when a fatal connection error ends this listener too.
	defer func() {
		l.Connected.Store(false)
	}()

	for failures := 0; ; {
		started := time.Now()
		err := l.session(ctx, events)
		l.Connected.Store(false)

		if ctx.Err() != nil {
			<-workerDone

			return nil
		}

		var failure *sessionError

		if errors.As(err, &failure) && failure.fatal {
			return err
		}

		if time.Since(started) > time.Minute {
			failures = 0
		}

		failures++

		if failures >= 10 {
			return errors.New("RTM connection failed repeatedly; check credentials, organization, group access and monitoring settings")
		}

		delay := min(l.ReconnectDelay*time.Duration(1<<min(failures-1, 5)), 30*time.Second)
		l.Logger.Warn("RTM disconnected; updates during downtime are not replayed", "retry_in", delay.String())

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			<-workerDone

			return nil
		case <-timer.C:
		}
	}
}

func (l *Listener) session(ctx context.Context, events chan<- string) error {
	u, _ := url.Parse(l.URL)
	q := u.Query()
	q.Set("organization_id", l.Organization)
	u.RawQuery = q.Encode()
	dialCtx, cancelDial := context.WithTimeout(ctx, 20*time.Second)
	conn, res, err := websocket.Dial(
		dialCtx,
		u.String(),
		&websocket.DialOptions{
			HTTPClient: &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			},
		},
	)

	cancelDial()

	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}

		return &sessionError{
			message: "RTM connection failed",
			fatal:   res != nil && (res.StatusCode == 401 || res.StatusCode == 403),
		}
	}

	defer conn.CloseNow()

	conn.SetReadLimit(8 << 20)

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := send(
		sessionCtx,
		conn,
		"login",
		"login",
		map[string]any{
			"token":                     "Basic " + l.Auth,
			"away":                      true,
			"customer_monitoring_level": "online",
			"pushes": map[string][]string{
				"3.6": {"segment_memberships_updated"},
			},
			"application": map[string]string{
				"name":    "hubspot-qualification-example",
				"version": "1.0",
			},
		},
	); err != nil {
		return err
	}

	if err := l.await(sessionCtx, conn, "login", events); err != nil {
		return err
	}

	l.Connected.Store(true)
	l.Logger.Info("RTM listening", "segment_id", l.Segment)

	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)

		ticker := time.NewTicker(l.PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				if send(sessionCtx, conn, "ping", "ping", map[string]any{}) != nil {
					conn.CloseNow()

					return
				}
			}
		}
	}()

	defer func() {
		cancel()
		<-heartbeatDone
	}()

	for {
		read, cancel := context.WithTimeout(sessionCtx, l.ReadTimeout)

		var f frame

		err := wsjson.Read(read, conn, &f)

		cancel()

		if err != nil {
			return errors.New("RTM read failed")
		}

		if f.Type == "response" && f.Action == "ping" && !f.Success {
			return errors.New("RTM heartbeat rejected")
		}

		l.dispatch(f, events)
	}
}

func send(ctx context.Context, conn *websocket.Conn, id, action string, payload any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if wsjson.Write(ctx, conn, map[string]any{
		"request_id": id,
		"action":     action,
		"payload":    payload,
	}) != nil {
		return errors.New("RTM write failed")
	}

	return nil
}

func (l *Listener) await(ctx context.Context, conn *websocket.Conn, id string, events chan<- string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for {
		var f frame

		if wsjson.Read(ctx, conn, &f) != nil {
			return errors.New("RTM setup response failed")
		}

		if f.Type == "response" && f.RequestID == id {
			if !f.Success {
				return &sessionError{
					message: "RTM setup rejected; check token scopes, group access and online monitoring availability",
					fatal:   true,
				}
			}

			return nil
		}

		l.dispatch(f, events)
	}
}

func (l *Listener) dispatch(f frame, events chan<- string) {
	if f.Type != "push" {
		return
	}

	if f.Action != "segment_memberships_updated" {
		return
	}

	var p push

	if json.Unmarshal(f.Payload, &p) != nil || p.CustomerID == "" {
		l.Failed.Add(1)
		l.Logger.Warn("discarded malformed segment push")

		return
	}

	if len(p.Added)+len(p.Removed) > 0 && !slices.Contains(p.Added, l.Segment) &&
		!slices.Contains(p.Removed, l.Segment) {
		return
	}

	if !slices.ContainsFunc(p.Memberships, func(m textapi.Membership) bool {
		return m.SegmentID == l.Segment
	}) {
		return
	}

	l.Received.Add(1)

	select {
	case events <- p.CustomerID:
	default:
		l.Failed.Add(1)
		l.Logger.Error("qualification event buffer full; update discarded")
	}
}
