package cmd

import (
	"strings"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func pdEntryOutcome(cmd *cobra.Command, res *ops.PDEntryResult, err error) error {
	if res != nil {
		output.SetResult(cmd.Context(), res)
		log.Info().Msgf("%s %s (partner %s)", res.Key, strings.ToLower(res.Action), res.Pid)
	}
	return err
}

// pdPartnerCommands are pd list and the alternative partner and authorized
// user commands.
func pdPartnerCommands() []*cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List the partners of the Partner Directory with parameter counts and content types",
		Long: `One line per partner ID: number of string and binary parameters, binary
content types, alternative partners and authorized users. Parameter values are
not downloaded; --sizes reads the binaries to add their total size.`,
		Example: `  cpictl pd list
  cpictl pd list --pid ACME --sizes`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			partners, err := ops.ListPDPartnersWith(cpi.NewPartnerDirectory(tenantExecuter(cmd)),
				ops.PDPartnersOptions{Sizes: config.GetBool(cmd, "sizes"), Pid: config.GetString(cmd, "pid")})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"partners": partners})
			for _, p := range partners {
				log.Info().Msgf("%-30s %4d string(s) %4d binar(ies) %-20s %d bytes", p.Pid, p.Strings, p.Binaries, strings.Join(p.ContentTypes, ","), p.BinaryBytes)
			}
			log.Info().Msgf("%d partner(s)", len(partners))
			return nil
		},
	}
	list.Flags().String("pid", "", "Only partner IDs containing this text")
	list.Flags().Bool("sizes", false, "Read the binary values to report their total size")

	alt := &cobra.Command{Use: "alternative-partners", Short: "List, create and delete alternative partners (agency/scheme/id -> partner ID)"}
	altList := &cobra.Command{
		Use: "list", Short: "List alternative partners", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			alts, err := ops.ListAlternativePartners(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "pid"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"alternativePartners": alts})
			for _, a := range alts {
				log.Info().Msgf("%-15s %-15s %-30s -> %s", a.Agency, a.Scheme, a.ID, a.Pid)
			}
			return nil
		},
	}
	altList.Flags().String("pid", "", "Only those of this partner ID")
	altCreate := &cobra.Command{
		Use: "create", Short: "Map agency/scheme/id to a partner ID", SilenceUsage: true,
		Example: `  cpictl pd alternative-partners create --agency EDI --scheme GLN --id 4000001000002 --pid ACME`,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.CreateAlternativePartner(cpi.NewPartnerDirectory(tenantExecuter(cmd)), cpi.AlternativePartner{Agency: config.GetString(cmd, "agency"),
				Scheme: config.GetString(cmd, "scheme"), ID: config.GetString(cmd, "id"), Pid: config.GetString(cmd, "pid")})
			return pdEntryOutcome(cmd, res, err)
		},
	}
	altDelete := &cobra.Command{
		Use: "delete", Short: "Delete an alternative partner (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !config.GetBool(cmd, "confirm") {
				return output.Usagef("messages identified by this alternative partner no longer resolve to a partner ID; repeat with --confirm")
			}
			res, err := ops.DeleteAlternativePartner(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "agency"),
				config.GetString(cmd, "scheme"), config.GetString(cmd, "id"))
			return pdEntryOutcome(cmd, res, err)
		},
	}
	for _, c := range []*cobra.Command{altCreate, altDelete} {
		c.Flags().String("agency", "", "Agency")
		c.Flags().String("scheme", "", "Scheme")
		c.Flags().String("id", "", "Identifier in the scheme")
	}
	altCreate.Flags().String("pid", "", "Partner ID")
	altDelete.Flags().Bool("confirm", false, "Confirm the deletion")
	alt.AddCommand(altList, altCreate, altDelete)

	users := &cobra.Command{Use: "authorized-users", Short: "List, create and delete authorized users (user -> partner ID)"}
	usersList := &cobra.Command{
		Use: "list", Short: "List authorized users", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := ops.ListAuthorizedUsers(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "pid"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"authorizedUsers": list})
			for _, u := range list {
				log.Info().Msgf("%-40s -> %s", u.User, u.Pid)
			}
			return nil
		},
	}
	usersList.Flags().String("pid", "", "Only those of this partner ID")
	usersCreate := &cobra.Command{
		Use: "create", Short: "Authorize a user for a partner ID", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.CreateAuthorizedUser(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "user"), config.GetString(cmd, "pid"))
			return pdEntryOutcome(cmd, res, err)
		},
	}
	usersCreate.Flags().String("user", "", "User (as authenticated by the sender adapter)")
	usersCreate.Flags().String("pid", "", "Partner ID")
	usersDelete := &cobra.Command{
		Use: "delete", Short: "Remove an authorized user (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !config.GetBool(cmd, "confirm") {
				return output.Usagef("the user can no longer act for its partner ID; repeat with --confirm")
			}
			res, err := ops.DeleteAuthorizedUser(cpi.NewPartnerDirectory(tenantExecuter(cmd)), config.GetString(cmd, "user"))
			return pdEntryOutcome(cmd, res, err)
		},
	}
	usersDelete.Flags().String("user", "", "User")
	usersDelete.Flags().Bool("confirm", false, "Confirm the deletion")
	users.AddCommand(usersList, usersCreate, usersDelete)

	return []*cobra.Command{list, alt, users}
}
