package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/openbekci/openbekci/server/internal/app"
	"github.com/openbekci/openbekci/server/internal/core/identity"
	"github.com/openbekci/openbekci/server/internal/core/platformadmin"
)

const adminPasswordEnv = "OPENBEKCI_ADMIN_PASSWORD" //nolint:gosec // name of an environment variable, not a credential

// newAdminCmd is the parent of the administration subcommands.
func newAdminCmd(load configLoader) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Administrative commands",
	}
	cmd.AddCommand(
		newCreatePlatformAdminCmd(load),
		newCreateOrgCmd(load),
		newSetPasswordCmd(load),
		newLoginLinkCmd(load),
		newDeliveryLogCmd(load),
		newExportSiteCmd(load),
		newRebuildChargeStatusCmd(load),
		newPostEntryCmd(load),
		newImportSiteCmd(load),
		newOffboardSiteCmd(load),
		newResealCmd(load),
		newPushCmd(load),
	)
	return cmd
}

// openAdmin loads the configuration and connects.
func openAdmin(cmd *cobra.Command, load configLoader) (*app.Admin, error) {
	cfg, err := load()
	if err != nil {
		return nil, err
	}
	return app.OpenAdmin(cmd.Context(), cfg, app.NewLogger(cmd.ErrOrStderr(), cfg.Debug))
}

// contactFlags registers --<prefix>email and --<prefix>phone.
func contactFlags(cmd *cobra.Command, prefix string, email, phone *string) {
	cmd.Flags().StringVar(email, prefix+"email", "", "email address")
	cmd.Flags().StringVar(phone, prefix+"phone", "", "phone number (E.164, or national format for the default country)")
}

// pickContact returns the single identifier given by an email or phone flag.
func pickContact(prefix, email, phone string) (string, error) {
	switch {
	case email != "" && phone != "":
		return "", fmt.Errorf("give --%semail or --%sphone, not both", prefix, prefix)
	case email != "":
		return email, nil
	case phone != "":
		return phone, nil
	}
	return "", fmt.Errorf("--%semail or --%sphone is required", prefix, prefix)
}

// resolvePassword returns the password from the flag, OPENBEKCI_ADMIN_PASSWORD
// or an interactive prompt (asked twice). Empty means none was given.
func resolvePassword(cmd *cobra.Command, flagValue string, required bool) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if v := os.Getenv(adminPasswordEnv); v != "" {
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		if required {
			return "", fmt.Errorf("no password: pass --password, set %s, or run in a terminal", adminPasswordEnv)
		}
		return "", nil
	}
	out := cmd.ErrOrStderr()
	prompt := "Password (empty for none, minimum 10 characters): "
	if required {
		prompt = "New password (minimum 10 characters): "
	}
	fmt.Fprint(out, prompt)
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(first) == 0 {
		if required {
			return "", errors.New("empty password")
		}
		return "", nil
	}
	fmt.Fprint(out, "Repeat password: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("the passwords do not match")
	}
	return string(first), nil
}

func newCreatePlatformAdminCmd(load configLoader) *cobra.Command {
	var email, phone, password string
	cmd := &cobra.Command{
		Use:   "create-platform-admin",
		Short: "Create an instance operator account (or promote an existing user)",
		Long: "Creates a user with platform administrator rights. The password comes from --password,\n" +
			"the " + adminPasswordEnv + " environment variable, or a prompt; with none, the account\n" +
			"is created without a password and a one-time sign-in link is printed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ident, err := pickContact("", email, phone)
			if err != nil {
				return err
			}
			pw, err := resolvePassword(cmd, password, false)
			if err != nil {
				return err
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			u, found, err := a.Identity.FindUser(ctx, ident)
			if err != nil {
				return err
			}
			if found {
				if err := a.Identity.PromotePlatformAdmin(ctx, u.ID); err != nil {
					return err
				}
				if pw != "" {
					if err := a.Identity.SetPasswordAdmin(ctx, u.ID, pw); err != nil {
						return err
					}
				}
				fmt.Fprintf(out, "user %s is now a platform administrator\n", u.ID)
				return nil
			}
			p := identity.CreateUserParams{Password: pw, IsPlatformAdmin: true}
			if email != "" {
				p.Email = email
			} else {
				p.Phone = phone
			}
			u, err = a.Identity.CreateUser(ctx, p)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "platform administrator %s created\n", u.ID)
			if pw == "" {
				return printLoginLink(cmd, a, u)
			}
			return nil
		},
	}
	contactFlags(cmd, "", &email, &phone)
	cmd.Flags().StringVar(&password, "password", "", "password (visible in the process list; prefer "+adminPasswordEnv+" or the prompt)")
	return cmd
}

func printLoginLink(cmd *cobra.Command, a *app.Admin, u identity.User) error {
	link, err := a.Identity.CreateLoginLink(cmd.Context(), u.ID, identity.ClientWeb)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "one-time sign-in link (valid %d minutes, single use):\n%s\n", int(identity.DefaultLinkTTL.Minutes()), link)
	return nil
}

func newCreateOrgCmd(load configLoader) *cobra.Command {
	var slug, name, country, locale, tz, adminName, adminEmail, adminPhone string
	cmd := &cobra.Command{
		Use:   "create-org",
		Short: "Create an organization with its first administrator",
		Long: "Creates the organization, a person for the administrator and an org_admin membership in one\n" +
			"transaction (nothing is left behind if any part fails). If the administrator has no account\n" +
			"yet it is created without a password. A one-time sign-in link is printed for a new and for an\n" +
			"existing administrator alike; `admin login-link` issues another.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ident, err := pickContact("admin-", adminEmail, adminPhone)
			if err != nil {
				return err
			}
			if slug == "" || name == "" {
				return errors.New("--slug and --name are required")
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			p := identity.CreateUserParams{Locale: locale}
			if adminEmail != "" {
				p.Email = adminEmail
			} else {
				p.Phone = adminPhone
			}
			resolve, err := a.Identity.PrepareFindOrCreateUser(ident, p)
			if err != nil {
				return err
			}
			display := adminName
			if display == "" {
				display = ident
			}
			// One transaction: the user (when new), the organization, the person and the
			// membership are committed together or not at all.
			org, err := a.Platform.CreateOrganization(ctx, platformadmin.CreateOrgParams{
				Slug: slug, Name: name, CountryCode: strings.ToUpper(country), DefaultLocale: locale, Timezone: tz,
				ResolveAdmin: resolve, AdminName: display,
			})
			if err != nil {
				return err
			}
			verb := "existing user"
			if org.AdminCreated {
				verb = "new user"
			}
			fmt.Fprintf(out, "organization %s (%s) created; %s %s is org_admin\n", org.Slug, org.ID, verb, org.AdminUserID)
			u, err := a.Identity.GetUser(ctx, org.AdminUserID)
			if err != nil {
				return err
			}
			// A link for the new and for the existing administrator alike: neither may be
			// left without a way in.
			return printLoginLink(cmd, a, u)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "short unique name used in URLs (lowercase letters, digits, hyphens)")
	cmd.Flags().StringVar(&name, "name", "", "display name")
	cmd.Flags().StringVar(&country, "country", "TR", "ISO 3166-1 alpha-2 country code")
	cmd.Flags().StringVar(&locale, "locale", "tr", "default locale")
	cmd.Flags().StringVar(&tz, "timezone", "Europe/Istanbul", "IANA time zone")
	cmd.Flags().StringVar(&adminName, "admin-name", "", "display name of the administrator (default: the email or phone)")
	contactFlags(cmd, "admin-", &adminEmail, &adminPhone)
	return cmd
}

func newSetPasswordCmd(load configLoader) *cobra.Command {
	var user, password string
	cmd := &cobra.Command{
		Use:   "set-password",
		Short: "Set a user's password and sign them out everywhere",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if user == "" {
				return errors.New("--user (email or phone) is required")
			}
			pw, err := resolvePassword(cmd, password, true)
			if err != nil {
				return err
			}
			a, err := openAdmin(cmd, load)
			if err != nil {
				return err
			}
			defer a.Close()
			u, found, err := a.Identity.FindUser(cmd.Context(), user)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("no user %q", user)
			}
			if err := a.Identity.SetPasswordAdmin(cmd.Context(), u.ID, pw); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "password of %s set; all their sessions were revoked\n", u.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&user, "user", "", "email or phone of the user")
	cmd.Flags().StringVar(&password, "password", "", "password (visible in the process list; prefer "+adminPasswordEnv+" or the prompt)")
	return cmd
}
