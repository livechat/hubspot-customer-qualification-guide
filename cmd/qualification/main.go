package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/livechat/hubspot-customer-qualification-example/internal/config"
	"github.com/livechat/hubspot-customer-qualification-example/internal/hubspot"
	"github.com/livechat/hubspot-customer-qualification-example/internal/importer"
	"github.com/livechat/hubspot-customer-qualification-example/internal/qualification"
	"github.com/livechat/hubspot-customer-qualification-example/internal/rtm"
	"github.com/livechat/hubspot-customer-qualification-example/internal/safehttp"
	"github.com/livechat/hubspot-customer-qualification-example/internal/textapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, os.Args[1:], logger); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprintln(os.Stdout, "Usage: qualification import [--dry-run] | qualification listen")

		return nil
	}

	if args[0] != "import" && args[0] != "listen" {
		return errors.New("unknown command; use import or listen")
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	dry := false

	if args[0] == "import" {
		flags.BoolVar(&dry, "dry-run", false, "read and validate without writing to Text")
	}

	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errors.New("invalid command flags")
	}

	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	c, err := config.Load(args[0] == "listen")
	if err != nil {
		return err
	}

	hs := &hubspot.Client{
		API: safehttp.New(c.HubSpotURL, "Bearer "+c.HubSpotToken),
	}
	text := &textapi.Client{
		API: safehttp.New(c.CDPURL, "Basic "+c.TextTokenBase64),
	}
	hs.API.Interval = c.Interval
	text.API.Interval = c.Interval

	if args[0] == "import" {
		start := time.Now()
		_, err := importer.Run(ctx, hs, text, dry, logger)
		logger.Info(
			"import finished",
			"dry_run", dry,
			"elapsed", time.Since(start).Round(time.Millisecond).String(),
			"complete", err == nil,
		)

		return err
	}

	if err := hs.ValidateProperty(ctx); err != nil {
		return err
	}

	segment, err := text.QualifiedSegment(ctx)
	if err != nil {
		return err
	}

	accounts := safehttp.New(c.AccountsURL, "Basic "+c.TextTokenBase64)

	organizationID, err := textapi.OrganizationID(ctx, accounts)
	if err != nil {
		return err
	}

	l := &rtm.Listener{
		URL:          c.RTMURL,
		Organization: organizationID,
		Auth:         c.TextTokenBase64,
		Segment:      segment,
		Logger:       logger,
		Syncer: &qualification.Service{
			Text:    text,
			HubSpot: hs,
			Segment: segment,
		},
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /healthz",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if !l.Connected.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})
	server := &http.Server{
		Addr:              ":" + c.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serveErr := make(chan error, 1)

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- errors.New("health server failed to start")

			cancel()
		}
	}()

	err = l.Run(ctx)

	cancel()

	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	_ = server.Shutdown(shutdown)

	select {
	case healthErr := <-serveErr:
		return healthErr
	default:
		return err
	}
}
