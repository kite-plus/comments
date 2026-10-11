package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kite-plus/comments/internal/model"
	"github.com/kite-plus/comments/internal/store"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "users", Short: "Manage reader accounts"}
	for _, grant := range []bool{true, false} {
		use, short := "promote-admin EMAIL|USER-ID", "Grant an account platform maintainer access"
		if !grant {
			use, short = "demote-admin EMAIL|USER-ID", "Remove an account's platform maintainer access"
		}
		cmd.AddCommand(&cobra.Command{
			Use: use, Short: short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, st, _, err := openStore(cmd.Context())
				if err != nil {
					return err
				}
				defer st.Close()
				var u model.User
				if strings.Contains(args[0], "@") {
					keys, err := cfg.Keys()
					if err != nil {
						return err
					}
					users, err := st.UsersByEmailHMAC(cmd.Context(), keys.EmailHMAC(args[0]))
					if err != nil {
						return err
					}
					if len(users) != 1 {
						return fmt.Errorf("%d accounts have this verified email; use the account ID", len(users))
					}
					u = users[0]
				} else if u, err = st.UserByPublicID(cmd.Context(), args[0]); err != nil {
					return err
				}
				if err := st.SetPlatformAdmin(cmd.Context(), u.ID, grant); err != nil {
					return err
				}
				printf(cmd, "done: %s (%s)\n", u.DisplayName, u.PublicID)
				return nil
			},
		})
	}
	return cmd
}

func newSitesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sites", Short: "Manage sites as the platform"}
	list := &cobra.Command{
		Use: "list", Short: "List sites, optionally by status", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, st, _, err := openStore(cmd.Context())
			if err != nil {
				return err
			}
			defer st.Close()
			status, _ := cmd.Flags().GetString("status")
			sites, err := st.AdminSites(cmd.Context(), status, nil, 1000)
			if err != nil {
				return err
			}
			if jsonOut(cmd) {
				out := []map[string]any{}
				for _, x := range sites {
					out = append(out, map[string]any{"id": x.PublicID, "name": x.Name, "status": x.Status, "origins": x.VerifiedOrigins()})
				}
				return writeJSON(cmd.OutOrStdout(), out)
			}
			for _, x := range sites {
				printf(cmd, "%s  %-15s %s  %s\n", x.PublicID, x.Status, x.Name, strings.Join(x.VerifiedOrigins(), " "))
			}
			return nil
		},
	}
	list.Flags().String("status", "", "only sites in this status")
	cmd.AddCommand(list)
	actions := map[string]struct {
		short string
		run   func(*cobra.Command, *store.Store, model.Site, string) error
	}{
		"approve": {"Approve a site in review", func(c *cobra.Command, st *store.Store, x model.Site, _ string) error {
			return st.ApproveSite(c.Context(), x.ID)
		}},
		"suspend": {"Suspend a site", func(c *cobra.Command, st *store.Store, x model.Site, note string) error {
			return st.SuspendSite(c.Context(), x.ID, note)
		}},
		"unsuspend": {"Lift a site's suspension", func(c *cobra.Command, st *store.Store, x model.Site, _ string) error {
			return st.UnsuspendSite(c.Context(), x.ID)
		}},
	}
	for name, a := range actions {
		sub := &cobra.Command{
			Use: name + " SITE-ID", Short: a.short, Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				_, st, _, err := openStore(cmd.Context())
				if err != nil {
					return err
				}
				defer st.Close()
				site, err := st.Site(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				note, _ := cmd.Flags().GetString("note")
				if err := a.run(cmd, st, site, note); err != nil {
					if errors.Is(err, store.ErrInvalid) {
						return fmt.Errorf("site %s is %s; nothing to %s", site.PublicID, site.Status, name)
					}
					return err
				}
				if name == "suspend" {
					_ = st.AddEvent(cmd.Context(), site.ID, nil, store.Event{Action: "platform_suspend_site", Detail: map[string]any{"note": note}})
				}
				site, err = st.Site(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				printf(cmd, "%s is now %s\n", site.PublicID, site.Status)
				return nil
			},
		}
		sub.Flags().String("note", "", "a note for the site's status")
		cmd.AddCommand(sub)
	}
	return cmd
}
