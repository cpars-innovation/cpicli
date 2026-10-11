package cmd

import (
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/cpi"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

// Runtime data commands: data stores, variables, JMS, number ranges, log
// files, idempotent repository, ID mapper.

func dataStoreKey(cmd *cobra.Command) cpi.DataStoreKey {
	return cpi.DataStoreKey{Name: config.GetString(cmd, "data-store"), IntegrationFlow: config.GetString(cmd, "artifact-id"), Type: config.GetString(cmd, "type")}
}

func addDataStoreFlags(c *cobra.Command) {
	c.Flags().String("data-store", "", "Data store name")
	c.Flags().String("artifact-id", "", "Integration flow of the data store (empty for global stores)")
	c.Flags().String("type", "", "Data store type (stores of adapters or steps, e.g. XI, AS4)")
}

func NewDataStoreCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "datastore",
		Short: "List data stores and their entries, read or delete an entry",
		Example: `  cpictl datastore list --overdue-only
  cpictl datastore entries --data-store Orders --artifact-id OrderIntake
  cpictl datastore get --data-store Orders --artifact-id OrderIntake --id e1 --out entry.xml
  cpictl datastore delete --data-store Orders --artifact-id OrderIntake --id e1 --confirm`,
	}
	list := &cobra.Command{
		Use: "list", Short: "List data stores with their number of entries", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			overdue, _ := cmd.Flags().GetBool("overdue-only")
			stores, err := cpi.NewStores(tenantExecuter(cmd)).DataStores(overdue)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"dataStores": stores})
			for _, s := range stores {
				log.Info().Msgf("%-30s %-30s %5d entries, %d overdue", s.Name, s.IntegrationFlow, s.Messages, s.OverdueMessages)
			}
			return nil
		},
	}
	list.Flags().Bool("overdue-only", false, "Only stores with overdue entries")

	entries := &cobra.Command{
		Use: "entries", Short: "List entries of a data store (or of all stores)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			overdue, _ := cmd.Flags().GetBool("overdue-only")
			res, err := ops.ListDataStoreEntries(tenantExecuter(cmd), dataStoreKey(cmd), config.GetString(cmd, "message-guid"), overdue, config.GetInt(cmd, "top"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, e := range res.Entries {
				log.Info().Msgf("%-40s %-10s %s %s", e.ID, e.Status, e.DataStoreName, e.MessageGuid)
			}
			log.Info().Msgf("%d of %d entries", len(res.Entries), res.Total)
			return nil
		},
	}
	addDataStoreFlags(entries)
	entries.Flags().String("message-guid", "", "Only entries written by this message")
	entries.Flags().Bool("overdue-only", false, "Only overdue entries")
	entries.Flags().Int("top", 100, "Maximum entries")

	get := &cobra.Command{
		Use: "get", Short: "Download the content of a data store entry", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetDataStoreEntry(tenantExecuter(cmd), config.GetString(cmd, "id"), dataStoreKey(cmd), contentLimit(cmd))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	addDataStoreFlags(get)
	get.Flags().String("id", "", "Entry ID")
	addContentFlags(get)

	del := &cobra.Command{
		Use: "delete", Short: "Delete a data store entry (requires --confirm)", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ok, _ := cmd.Flags().GetBool("confirm"); !ok {
				return output.Usagef("deleting %s needs --confirm", config.GetString(cmd, "id"))
			}
			res, err := ops.DeleteDataStoreEntry(tenantExecuter(cmd), config.GetString(cmd, "id"), dataStoreKey(cmd))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			log.Info().Msgf("Deleted %s from %s", res["id"], res["dataStore"])
			return nil
		},
	}
	addDataStoreFlags(del)
	del.Flags().String("id", "", "Entry ID")
	del.Flags().Bool("confirm", false, "Confirm the deletion")

	c.AddCommand(list, entries, get, del)
	return c
}

func NewVariablesCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "variables", Short: "List global and integration flow variables", SilenceUsage: true,
		Example: `  cpictl variables --artifact-id OrderIntake
  cpictl variables get --name lastRun --artifact-id OrderIntake`,
		RunE: func(cmd *cobra.Command, args []string) error {
			vars, err := ops.ListVariables(tenantExecuter(cmd), config.GetString(cmd, "artifact-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"variables": vars})
			for _, v := range vars {
				log.Info().Msgf("%-30s %-30s %s", v.Name, v.IntegrationFlow, v.Visibility)
			}
			return nil
		},
	}
	c.Flags().String("artifact-id", "", "Only this flow's variables (plus global ones)")
	get := &cobra.Command{
		Use: "get", Short: "Read the value of a variable", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetVariable(tenantExecuter(cmd), config.GetString(cmd, "name"), config.GetString(cmd, "artifact-id"), contentLimit(cmd))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	get.Flags().String("name", "", "Variable name")
	get.Flags().String("artifact-id", "", "Integration flow (empty: global variable)")
	addContentFlags(get)
	c.AddCommand(get)
	return c
}

func NewJMSCommand() *cobra.Command {
	c := &cobra.Command{Use: "jms", Short: "JMS queues and broker capacity"}
	queues := &cobra.Command{
		Use: "queues", Short: "List JMS queues, fullest first", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			qs, err := ops.ListQueues(tenantExecuter(cmd), config.GetString(cmd, "prefix"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"queues": qs})
			for _, q := range qs {
				log.Info().Msgf("%-50s %6d messages active=%v", q.Name, q.Messages, q.Active)
			}
			return nil
		},
	}
	queues.Flags().String("prefix", "", "Only queues whose name starts with this")
	broker := &cobra.Command{
		Use: "broker", Short: "Show JMS broker capacity and usage", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := cpi.NewStores(tenantExecuter(cmd)).Broker()
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), b)
			log.Info().Msgf("capacity %d of %d, queues %d of %d (ok %d, warning %d, error %d)", b.Capacity, b.MaxCapacity, b.Queues, b.MaxQueues, b.CapacityOK, b.CapacityWarning, b.CapacityError)
			return nil
		},
	}
	c.AddCommand(queues, broker)
	return c
}

func NewNumberRangesCommand() *cobra.Command {
	return &cobra.Command{
		Use: "number-ranges", Short: "List number ranges", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			list, err := cpi.NewStores(tenantExecuter(cmd)).NumberRanges()
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"numberRanges": list})
			for _, n := range list {
				log.Info().Msgf("%-30s %s (%s..%s)", n.Name, n.CurrentValue, n.MinValue, n.MaxValue)
			}
			return nil
		},
	}
}

func NewLogFilesCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "log-files", Short: "List system and HTTP log files of the runtime", SilenceUsage: true,
		Example: `  cpictl log-files --type http --since 2h
  cpictl log-files get --name http_access_2026-10-05.log --application it-cpi --tail-bytes 20000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			since, err := ops.ParseTimeArg(config.GetString(cmd, "since"), time.Now())
			if err != nil {
				return output.Usagef("invalid --since: %v", err)
			}
			res, err := ops.ListLogFiles(tenantExecuter(cmd), config.GetString(cmd, "type"), since)
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), res)
			for _, f := range res.Files {
				log.Info().Msgf("%-50s %-20s %-8s %d bytes", f.Name, f.Application, f.LogFileType, f.Size)
			}
			return nil
		},
	}
	c.Flags().String("type", "", "Log file type, e.g. http or trace")
	c.Flags().String("since", "", "Only files modified after this time (duration like 2h or RFC 3339)")
	get := &cobra.Command{
		Use: "get", Short: "Print the end of a log file", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := ops.GetLogFile(tenantExecuter(cmd), config.GetString(cmd, "name"), config.GetString(cmd, "application"), config.GetInt(cmd, "tail-bytes"))
			if err != nil {
				return err
			}
			return emitContent(cmd, res, res.Content)
		},
	}
	get.Flags().String("name", "", "Log file name")
	get.Flags().String("application", "", "Application of the log file")
	get.Flags().Int("tail-bytes", 65536, "Bytes from the end of the file")
	get.Flags().String("out", "", "Write the content to this file instead of stdout / the JSON result")
	c.AddCommand(get, newLogArchiveCommand())
	return c
}

func NewIdempotentCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "idempotent", Short: "List idempotent repository entries (messages or files skipped as duplicates)", SilenceUsage: true,
		Example: `  cpictl idempotent --id in/orders_20261005.csv --component SFTP`,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := ops.ListIdempotentEntries(tenantExecuter(cmd), config.GetString(cmd, "id"), config.GetString(cmd, "component"), config.GetString(cmd, "source"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"entries": entries})
			for _, e := range entries {
				log.Info().Msgf("%-8s %-50s %s", e.Component, e.Entry, e.Source)
			}
			return nil
		},
	}
	c.Flags().String("id", "", "Entry ID (SFTP: <directory>/<file name>, XI: message ID)")
	c.Flags().String("component", "", "Only this component, e.g. SFTP or XI")
	c.Flags().String("source", "", "Only sources containing this text")
	return c
}

func NewIDMappingsCommand() *cobra.Command {
	c := &cobra.Command{
		Use: "id-mappings", Short: "Show ID mapper entries of a source or target ID", SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := ops.ListIDMappings(tenantExecuter(cmd), config.GetString(cmd, "source-id"), config.GetString(cmd, "target-id"))
			if err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"mappings": m})
			for _, e := range m {
				log.Info().Msgf("%s -> %s (%s)", e.FromID, e.ToID, e.Mapper)
			}
			return nil
		},
	}
	c.Flags().String("source-id", "", "Source ID")
	c.Flags().String("target-id", "", "Target ID")
	return c
}
