package cmd

import (
	"fmt"
	"os"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/idoc"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// NewIDocCommand groups the local IDoc helpers.
func NewIDocCommand() *cobra.Command {
	c := &cobra.Command{
		Use:         "idoc",
		Short:       "IDoc test messages: write an IDoc XML skeleton",
		Annotations: map[string]string{annotationOffline: "true"},
	}
	c.AddCommand(newIDocTemplateCommand())
	return c
}

func newIDocTemplateCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "template",
		Short: "Write an IDoc XML skeleton (control record EDI_DC40) to fill in",
		Long: `Write a minimal IDoc XML message: the control record EDI_DC40 with the standard
fields and a commented placeholder where the data segments go. It is a skeleton
to fill: add the segments of the IDoc type yourself (their names and fields are
in the IDoc type's documentation, transaction WE60 on the SAP system), then send
it with 'cpictl send --idoc FILE'.

DOCNUM defaults to 0000000000000001; use a new number per test message when the
flow or the receiver checks for duplicates. DIRECT 1 is an IDoc sent by the SAP
system (outbound), 2 one sent to it.`,
		Example: `  cpictl idoc template --type ORDERS05 --mestyp ORDERS --sndprn S4HCLNT100 --rcvprn CPI --out orders.xml
  cpictl send --artifact-id Orders_IDoc_In --idoc orders.xml --wait 60s`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		Annotations:  map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := idoc.Template(idoc.TemplateOptions{
				IDocType: config.GetString(cmd, "type"), Extension: config.GetString(cmd, "cimtyp"), MessageType: config.GetString(cmd, "mestyp"),
				SenderPartner: config.GetString(cmd, "sndprn"), SenderPartnerType: config.GetString(cmd, "sndprt"), SenderPort: config.GetString(cmd, "sndpor"),
				ReceiverPartner: config.GetString(cmd, "rcvprn"), ReceiverPartnerType: config.GetString(cmd, "rcvprt"), ReceiverPort: config.GetString(cmd, "rcvpor"),
				DocNum: config.GetString(cmd, "docnum"), Direct: config.GetString(cmd, "direct"),
			})
			if err != nil {
				return output.Usage(err)
			}
			if out := config.GetString(cmd, "out"); out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return err
				}
				output.SetResult(cmd.Context(), map[string]any{"file": out, "size": len(data)})
				log.Info().Msgf("IDoc skeleton written to %s: add the data segments of %s", out, config.GetString(cmd, "type"))
				return nil
			}
			if outputFormat(cmd) == output.FormatJSON {
				output.SetResult(cmd.Context(), map[string]any{"content": string(data)})
				return nil
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	c.Flags().String("type", "", "IDoc type (IDOCTYP), e.g. ORDERS05")
	c.Flags().String("cimtyp", "", "IDoc extension (CIMTYP), if any")
	c.Flags().String("mestyp", "", "Message type (MESTYP), e.g. ORDERS")
	c.Flags().String("sndprn", "", "Sender partner number (SNDPRN)")
	c.Flags().String("sndprt", "LS", "Sender partner type (SNDPRT)")
	c.Flags().String("sndpor", "", "Sender port (SNDPOR)")
	c.Flags().String("rcvprn", "", "Receiver partner number (RCVPRN)")
	c.Flags().String("rcvprt", "LS", "Receiver partner type (RCVPRT)")
	c.Flags().String("rcvpor", "", "Receiver port (RCVPOR)")
	c.Flags().String("docnum", "", "IDoc number (DOCNUM, default 0000000000000001)")
	c.Flags().String("direct", "1", "Direction (DIRECT): 1 outbound from the SAP system, 2 inbound")
	c.Flags().String("out", "", "Write the skeleton to this file instead of stdout")
	_ = c.MarkFlagRequired("type")
	_ = c.MarkFlagRequired("mestyp")
	return c
}
