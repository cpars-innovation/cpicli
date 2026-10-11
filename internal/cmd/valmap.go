package cmd

import (
	"fmt"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewValMapCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "valmap",
		Short: "Read and change the values of value mappings",
		Long: `A value mapping artifact maps values between agencies and identifiers (schemas),
e.g. SAP:Country -> Partner:CountryCode. get shows each schema with its value
pairs and default values; set adds a value pair or changes the target value of a
source value in an existing schema. The runtime uses a change after the value
mapping is deployed (cpictl deploy --artifact-type ValueMapping).

New schemas (agency/identifier pairs) are added in the Web UI or by uploading the
artifact. Deleting single value pairs is not offered: the API's DeleteValMaps
call is not documented well enough to use safely.`,
		Example: `  cpictl valmap list --package-id Orders
  cpictl valmap get --id Country_Codes
  cpictl valmap set --id Country_Codes --src-agency SAP --src-id Country --tgt-agency Partner --tgt-id CountryCode \
    --src-value DE --tgt-value GER --confirm`,
	}
	list := &cobra.Command{
		Use: "list", Short: "List value mapping artifacts", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			vms, err := ops.ListValueMappings(tenantExecuter(cmd), config.GetString(cmd, "package-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"valueMappings": vms})
			for _, v := range vms {
				log.Info().Msgf("%-40s %-10s %s", v.ID, v.Version, v.PackageID)
			}
			log.Info().Msgf("%d value mapping(s)", len(vms))
			return nil
		},
	}
	list.Flags().String("package-id", "", "Only value mappings of this package")

	get := &cobra.Command{
		Use: "get", Short: "Show the schemas and value pairs of a value mapping", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			vm, err := ops.GetValueMapping(tenantExecuter(cmd), config.GetString(cmd, "id"), valMapFilter(cmd))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), vm)
			for _, s := range vm.Schemas {
				log.Info().Msgf("%s (%d value(s))", s.ValMapSchema, len(s.Values))
				for _, v := range s.Values {
					log.Info().Msgf("  %-30s -> %s", v.SrcValue, v.TgtValue)
				}
				for _, v := range s.Defaults {
					log.Info().Msgf("  default: %s -> %s", v.SrcValue, v.TgtValue)
				}
			}
			return nil
		},
	}
	get.Flags().String("id", "", "Value mapping ID")
	addSchemaFlags(get, "Only schemas with this %s")

	set := &cobra.Command{
		Use: "set", Short: "Add a value pair or change a target value (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			id := config.GetString(cmd, "id")
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("set changes value mapping %s on the tenant; add --confirm", id)
			}
			f := valMapFilter(cmd)
			res, err := ops.SetValueMapping(tenantExecuter(cmd), ops.SetValueMappingRequest{ID: id,
				SrcAgency: f.SrcAgency, SrcID: f.SrcID, TgtAgency: f.TgtAgency, TgtID: f.TgtID,
				SrcValue: config.GetString(cmd, "src-value"), TgtValue: config.GetString(cmd, "tgt-value")})
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			switch res.Action {
			case "unchanged":
				log.Info().Msgf("%s: %s -> %s already set", res.Schema, res.SrcValue, res.TgtValue)
			case "changed":
				log.Info().Msgf("%s: %s -> %s (was %s). Deploy %s to use it", res.Schema, res.SrcValue, res.TgtValue, res.Previous, id)
			default:
				log.Info().Msgf("%s: added %s -> %s. Deploy %s to use it", res.Schema, res.SrcValue, res.TgtValue, id)
			}
			return nil
		},
	}
	set.Flags().String("id", "", "Value mapping ID")
	addSchemaFlags(set, "Schema: %s")
	set.Flags().String("src-value", "", "Source value")
	set.Flags().String("tgt-value", "", "Target value")
	set.Flags().Bool("confirm", false, "Confirm the change on the tenant")

	c.AddCommand(list, get, set)
	return c
}

// addSchemaFlags adds the flags naming a schema; help is a format with %s
// for "source agency" and so on.
func addSchemaFlags(c *cobra.Command, help string) {
	for flag, what := range map[string]string{"src-agency": "source agency", "src-id": "source identifier",
		"tgt-agency": "target agency", "tgt-id": "target identifier"} {
		c.Flags().String(flag, "", fmt.Sprintf(help, what))
	}
}

func valMapFilter(cmd *cobra.Command) ops.ValMapFilter {
	return ops.ValMapFilter{SrcAgency: config.GetString(cmd, "src-agency"), SrcID: config.GetString(cmd, "src-id"),
		TgtAgency: config.GetString(cmd, "tgt-agency"), TgtID: config.GetString(cmd, "tgt-id")}
}
