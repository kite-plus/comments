package cli

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/kite-plus/comments/internal/devblogs"
	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

// newDevCmd holds development-only commands; each refuses to run unless
// COMMENTS_DEV_HTTP_ORIGINS is on, which config refuses on https.
func newDevCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dev", Short: "Local development helpers", Hidden: true}

	seed := &cobra.Command{
		Use:   "seed",
		Short: "Register the two local test blogs as verified, active sites",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, st, _, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()
			if !cfg.DevHTTPOrigins {
				return errors.New("dev seed needs COMMENTS_DEV_HTTP_ORIGINS=true")
			}
			a, _ := cmd.Flags().GetString("blog-a")
			b, _ := cmd.Flags().GetString("blog-b")
			for _, x := range []struct{ id, name, origin string }{
				{devblogs.SiteA, "Blog A (dev)", a},
				{devblogs.SiteB, "Blog B (dev)", b},
			} {
				site, err := st.Site(cmd.Context(), x.id)
				if errors.Is(err, store.ErrNotFound) {
					site, err = st.CreateSite(cmd.Context(), store.NewSite{Name: x.name, Origins: []string{x.origin}, Verified: true, PublicID: x.id})
				}
				if err != nil {
					return err
				}
				printf(cmd, "%s %s %s %v\n", site.PublicID, site.Status, site.Name, site.VerifiedOrigins())
			}
			owner, _ := cmd.Flags().GetString("owner-email")
			if owner != "" {
				keys, err := cfg.Keys()
				if err != nil {
					return err
				}
				users, err := st.UsersByEmailHMAC(cmd.Context(), keys.EmailHMAC(owner))
				if err != nil || len(users) != 1 {
					return errors.New("no single account with that verified email; sign in once first")
				}
				site, err := st.Site(cmd.Context(), devblogs.SiteA)
				if err != nil {
					return err
				}
				if err := st.AddMember(cmd.Context(), site.ID, users[0].ID, model.RoleOwner); err != nil {
					return err
				}
				printf(cmd, "%s owns %s\n", users[0].DisplayName, site.Name)
			}
			return nil
		},
	}
	seed.Flags().String("blog-a", "http://blog-a.localhost:8301", "blog A's origin")
	seed.Flags().String("blog-b", "http://blog-b.localhost:8302", "blog B's origin")
	seed.Flags().String("owner-email", "", "make this verified account blog A's owner")
	cmd.AddCommand(seed)

	blogs := &cobra.Command{
		Use:   "blogs",
		Short: "Serve the two local test blogs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if !cfg.DevHTTPOrigins {
				return errors.New("dev blogs needs COMMENTS_DEV_HTTP_ORIGINS=true")
			}
			a, _ := cmd.Flags().GetString("a")
			b, _ := cmd.Flags().GetString("b")
			servers := []*http.Server{
				{Addr: a, Handler: devblogs.A(cfg.PublicURL).Handler(), ReadHeaderTimeout: 5 * time.Second},
				{Addr: b, Handler: devblogs.B(cfg.PublicURL).Handler(), ReadHeaderTimeout: 5 * time.Second},
			}
			errc := make(chan error, len(servers))
			for _, s := range servers {
				go func() { errc <- s.ListenAndServe() }()
			}
			printf(cmd, "blog A: http://blog-a.localhost%s\nblog B: http://blog-b.localhost%s\n", a[len("127.0.0.1"):], b[len("127.0.0.1"):])
			select {
			case err := <-errc:
				return err
			case <-cmd.Context().Done():
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, s := range servers {
					_ = s.Shutdown(ctx)
				}
				return nil
			}
		},
	}
	blogs.Flags().String("a", "127.0.0.1:8301", "blog A's listen address")
	blogs.Flags().String("b", "127.0.0.1:8302", "blog B's listen address")
	cmd.AddCommand(blogs)
	return cmd
}
