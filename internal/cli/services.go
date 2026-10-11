package cli

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/kite-plus/comments/internal/api"
	"github.com/kite-plus/comments/internal/buildinfo"
	"github.com/kite-plus/comments/internal/idp"
	"github.com/kite-plus/comments/internal/mailer"
	"github.com/kite-plus/comments/internal/oidc"
	"github.com/kite-plus/comments/internal/verify"
	"github.com/kite-plus/comments/internal/worker"
	"github.com/kite-plus/comments/web"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, st, log, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()
			keys, err := cfg.Keys()
			if err != nil {
				return err
			}
			if err := cfg.RequireOIDC(); err != nil {
				return err
			}
			srv := &api.Server{
				Store: st, Keys: keys,
				OIDC: oidc.New(oidc.Config{Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
					RedirectURL: cfg.PublicURL + "/auth/callback"}),
				Fetcher: verify.NewFetcher(cfg.AllowPrivateNetworks), PublicURL: cfg.PublicURL, Issuer: cfg.OIDCIssuer,
				IDPWebhookSecret: cfg.IDPWebhookSecret, TrustedProxies: cfg.TrustedProxies, DevHTTPOrigins: cfg.DevHTTPOrigins,
				Widget: web.Widget(), Console: web.Console(), Log: log,
			}
			h, err := srv.Handler()
			if err != nil {
				return err
			}
			// Imports upload up to 50 MB, hence the long read timeout.
			hs := &http.Server{Addr: cfg.HTTPAddr, Handler: h, ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second}
			errc := make(chan error, 1)
			go func() { errc <- hs.ListenAndServe() }()
			log.Info("serving", "addr", cfg.HTTPAddr, "public_url", cfg.PublicURL, "version", buildinfo.Version,
				"dev_http_origins", cfg.DevHTTPOrigins)
			select {
			case err := <-errc:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			case <-ctx.Done():
				shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				log.Info("shutting down")
				return hs.Shutdown(shutdown)
			}
		},
	}
}

func newWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker",
		Short: "Send mail and run background maintenance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg, st, log, err := openStore(ctx)
			if err != nil {
				return err
			}
			defer st.Close()
			keys, err := cfg.Keys()
			if err != nil {
				return err
			}
			w := &worker.Worker{Store: st, Keys: keys, PublicURL: cfg.PublicURL, Issuer: cfg.OIDCIssuer,
				Fetcher: verify.NewFetcher(cfg.AllowPrivateNetworks), Log: log}
			if cfg.SMTPURL != "" {
				host := "localhost"
				if u, err := url.Parse(cfg.PublicURL); err == nil {
					host = u.Hostname()
				}
				sender, err := mailer.NewSender(cfg.SMTPURL, cfg.MailFrom, host)
				if err != nil {
					return err
				}
				w.Mailer = sender
			}
			if cfg.IDPAPIToken != "" {
				w.IDP = &idp.Client{BaseURL: cfg.IDPAPIURL, Token: cfg.IDPAPIToken}
			}
			log.Info("worker started", "mail", w.Mailer != nil, "reconcile", w.IDP != nil, "version", buildinfo.Version)
			return w.Run(ctx)
		},
	}
}

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "migrate", Short: "Manage the database schema"}
	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Apply every pending migration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, st, _, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.Migrate(cmd.Context()); err != nil {
				return err
			}
			printf(cmd, "migrations applied\n")
			return nil
		},
	})
	return cmd
}
