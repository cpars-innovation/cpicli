package cmd

import (
	"cmp"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cpars-innovation/cpicli/internal/config"
	"github.com/cpars-innovation/cpicli/internal/output"
	"github.com/cpars-innovation/cpicli/pkg/catalog"
	"github.com/cpars-innovation/cpicli/pkg/ops"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

func NewComponentsCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "components",
		Short: "Component catalogue: the adapter and step versions the tenant offers",
		Long: `The component catalogue (.cpi/components.json) lists the adapter and step
variants (cmdVariantUri) and versions the tenant's integration flow editor
offers. Flows built with them pass the tenant's Check; a version newer than the
tenant offers fails there (lint rule component-newer-than-tenant).

It is built from two sources:
  import  the editor metadata of the Web UI: in the browser's developer tools,
          open an integration flow and save the response of
          /api/1.0/metadata?profileID=... (it needs the browser login, so
          cpictl cannot fetch it). Authoritative: what the tenant offers today.
  learn   the variants existing flows use (versions the tenant accepted); also
          done by 'cpictl discover'. Automatic, but only knows what is in use.
          From the tenant it also records the custom adapters deployed there
          (runtime artifacts of an adapter type) with their version.
Entries from the editor metadata win over learned ones.`,
		Example: `  cpictl components import metadata.json
  cpictl components learn                 # all flows of the tenant
  cpictl components learn --dir ./content # local flows, offline
  cpictl components list --name HTTP`,
	}
	c.PersistentFlags().String("file", ops.ComponentsFile, "Catalogue file (config: components.file)")

	imp := &cobra.Command{
		Use: "import <editor-metadata.json|->", Short: "Build the catalogue from the Web UI's editor metadata", Args: cobra.ExactArgs(1), SilenceUsage: true,
		Annotations: map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var data []byte
			var err error
			if args[0] == "-" {
				data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 64<<20))
			} else {
				data, err = os.ReadFile(args[0])
			}
			if err != nil {
				return output.Usage(err)
			}
			cat, err := catalog.Import(data)
			if err != nil {
				return output.Usage(err)
			}
			file := componentsFile(cmd)
			// keep what was learned before
			if old, err := catalog.Load(file); err == nil {
				var uses []catalog.Use
				for _, a := range old.Adapters {
					if a.Source == catalog.SourceLearned {
						uses = append(uses, catalog.Use{URI: a.VariantURI, Adapter: a.Name, Direction: a.Direction})
					}
				}
				for _, s := range old.Steps {
					if s.Source == catalog.SourceLearned {
						uses = append(uses, catalog.Use{URI: s.VariantURI, Type: s.Type})
					}
				}
				cat.Learn(uses)
				cat.Tenant = old.Tenant
			}
			cat.Tenant = cmp.Or(config.GetString(cmd, "tenant"), cat.Tenant)
			cat.GeneratedAt = time.Now().UTC().Truncate(time.Second)
			if err := saveCatalog(cat, file); err != nil {
				return err
			}
			output.SetResult(cmd.Context(), map[string]any{"file": file, "adapters": len(cat.Adapters), "steps": len(cat.Steps), "sources": cat.Sources})
			log.Info().Msgf("%d adapter and %d step variants written to %s", len(cat.Adapters), len(cat.Steps), file)
			return nil
		},
	}
	imp.Flags().String("tenant", "", "Name of the tenant the metadata is from (e.g. the profile or tier)")

	learn := &cobra.Command{
		Use: "learn", Short: "Add the variants existing flows use (tenant, or --dir offline)", SilenceUsage: true,
		Annotations: map[string]string{annotationOffline: "dir"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var d *ops.Discovery
			var err error
			tenant := ""
			if dir := config.GetString(cmd, "dir"); dir != "" {
				d, err = ops.DiscoverDir(cmd.Context(), dir)
			} else {
				tenant = config.GetString(cmd, "tmn-host")
				d, err = ops.DiscoverTenant(cmd.Context(), tenantExecuter(cmd), tenant, ops.DiscoverOptions{PackageIDs: config.GetStringSlice(cmd, "package-ids")})
			}
			if err != nil {
				return err
			}
			file := componentsFile(cmd)
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return err
			}
			cat, err := ops.LearnComponents(d, file, tenant)
			if err != nil {
				return err
			}
			if tenant != "" {
				deployed, err := ops.DeployedAdapters(tenantExecuter(cmd))
				if err != nil {
					return err
				}
				cat.DeployedAdapters = deployed
				if err := cat.Save(file); err != nil {
					return err
				}
				for _, a := range deployed {
					log.Info().Msgf("Deployed custom adapter %s %s (%s)", a.ID, a.Version, a.Status)
				}
			}
			output.SetResult(cmd.Context(), map[string]any{"file": file, "iflows": len(d.IFlows), "adapters": len(cat.Adapters), "steps": len(cat.Steps), "sources": cat.Sources})
			log.Info().Msgf("Learned from %d integration flow(s): %d adapter and %d step variants in %s", len(d.IFlows), len(cat.Adapters), len(cat.Steps), file)
			return nil
		},
	}
	learn.Flags().String("dir", "", "Learn from this local content tree instead of the tenant")
	learn.Flags().StringSlice("package-ids", nil, "Only these packages (default: all)")

	list := &cobra.Command{
		Use: "list", Short: "List the catalogue (filter by name, kind, direction)", SilenceUsage: true,
		Annotations: map[string]string{annotationOffline: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			cat, err := catalog.Load(componentsFile(cmd))
			if errors.Is(err, fs.ErrNotExist) {
				return output.Usagef("no component catalogue at %s: run 'cpictl components import' or 'cpictl components learn'", componentsFile(cmd))
			}
			if err != nil {
				return err
			}
			res := ops.FilterComponents(cat, config.GetString(cmd, "name"), config.GetString(cmd, "kind"), config.GetString(cmd, "direction"))
			output.SetResult(cmd.Context(), res)
			for _, a := range res.Adapters {
				log.Info().Msgf("adapter %-22s %-9s %-10s %s", a.Name, a.Direction, a.Version, a.VariantURI)
			}
			for _, a := range res.DeployedAdapters {
				log.Info().Msgf("deployed %-21s %-10s %s", a.ID, a.Version, a.Status)
			}
			for _, s := range res.Steps {
				log.Info().Msgf("step    %-32s %-10s %s", s.Name, s.Version, s.VariantURI)
			}
			log.Info().Msgf("%d adapter and %d step variants (%s; %s)", len(res.Adapters), len(res.Steps), strings.Join(cat.Sources, ", "), cat.GeneratedAt.Format(time.DateOnly))
			return nil
		},
	}
	list.Flags().String("name", "", "Only variants whose name contains this (case-insensitive)")
	list.Flags().String("kind", "", "adapter or step")
	list.Flags().String("direction", "", "Sender or Receiver (adapters)")

	c.AddCommand(imp, learn, list)
	return c
}

func componentsFile(cmd *cobra.Command) string {
	return config.GetStringWithFallback(cmd, "file", "components.file")
}

func saveCatalog(cat *catalog.Catalog, file string) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return cat.Save(file)
}
