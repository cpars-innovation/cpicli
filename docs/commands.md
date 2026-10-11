# Command reference

<!-- Generated from the CLI by `go test ./internal/cmd -run TestCommandReference -update`. Do not edit. -->

Every flag can also be set with an environment variable (`CPICTL_` + flag name in upper case, `-` replaced by `_`) or as a top-level key in the config file. See [configuration.md](configuration.md).

| Command | Description |
|---------|-------------|
| [`artifacts`](#artifacts) | List designtime artifacts of a package |
| [`compare`](#compare) | Compare two tiers, Git refs or content trees per artifact (content, version, parameters, files) |
| [`components`](#components) | Component catalogue: the adapter and step versions the tenant offers |
| [`components import`](#components-import) | Build the catalogue from the Web UI's editor metadata |
| [`components learn`](#components-learn) | Add the variants existing flows use (tenant, or --dir offline) |
| [`components list`](#components-list) | List the catalogue (filter by name, kind, direction) |
| [`config-generate`](#config-generate) | Generate or refresh the orchestrator deployment config from a packages directory |
| [`configure`](#configure) | Set artifact parameters from YAML files and optionally deploy |
| [`configure pull`](#configure-pull) | Write current tenant parameter values into configure YAML files |
| [`credentials`](#credentials) | List and deploy user credentials, OAuth2 client credentials and secure parameters |
| [`credentials apply`](#credentials-apply) | Create or update all credentials of a YAML file |
| [`credentials delete`](#credentials-delete) | Delete a credential (requires --confirm) |
| [`credentials list`](#credentials-list) | List credentials (names and metadata, never secrets) |
| [`credentials set-oauth2`](#credentials-set-oauth2) | Create or update an OAuth2 client credential |
| [`credentials set-secure-param`](#credentials-set-secure-param) | Create or update a secure parameter (Neo environment) |
| [`credentials set-user`](#credentials-set-user) | Create or update a user credential |
| [`datastore`](#datastore) | List data stores and their entries, read or delete an entry |
| [`datastore delete`](#datastore-delete) | Delete a data store entry (requires --confirm) |
| [`datastore entries`](#datastore-entries) | List entries of a data store (or of all stores) |
| [`datastore get`](#datastore-get) | Download the content of a data store entry |
| [`datastore list`](#datastore-list) | List data stores with their number of entries |
| [`deploy`](#deploy) | Deploy designtime artifacts and wait for the result |
| [`discover`](#discover) | Inventory existing integration flows to derive conventions |
| [`doctor`](#doctor) | Check the setup: configuration, connection and which API areas the credentials can use |
| [`download`](#download) | Download a designtime artifact and extract it into a directory |
| [`drift`](#drift) | Compare local artifacts with their designtime and runtime state on the tenant |
| [`endpoints`](#endpoints) | List the URLs of deployed integration flows |
| [`graph`](#graph) | Query the content graph written by discover (offline) |
| [`graph build`](#graph-build) | Write graph.json from an existing discovery.json |
| [`graph neighbors`](#graph-neighbors) | Show what a node is connected to |
| [`graph path`](#graph-path) | Find the shortest connection between two nodes |
| [`graph search`](#graph-search) | Find nodes by key, name or attribute |
| [`guidelines`](#guidelines) | Check an integration flow against the design guidelines activated on the tenant |
| [`id-mappings`](#id-mappings) | Show ID mapper entries of a source or target ID |
| [`idempotent`](#idempotent) | List idempotent repository entries (messages or files skipped as duplicates) |
| [`iflow`](#iflow) | Work with integration flow files (copy a template under a new ID, lay out the diagram) |
| [`iflow copy`](#iflow-copy) | Copy an integration flow under a new ID, name and sender address |
| [`iflow layout`](#iflow-layout) | Lay out the diagram of integration flows: steps in flow order, no overlaps, right-angled lines (local files) |
| [`jms`](#jms) | JMS queues and broker capacity |
| [`jms broker`](#jms-broker) | Show JMS broker capacity and usage |
| [`jms queues`](#jms-queues) | List JMS queues, fullest first |
| [`keystore`](#keystore) | List keystore entries, check expiry, export and import certificates |
| [`keystore export-cert`](#keystore-export-cert) | Export the certificate of a keystore entry as PEM |
| [`keystore import-cert`](#keystore-import-cert) | Import a certificate (PEM or DER) into the tenant keystore |
| [`keystore list`](#keystore-list) | List keystore entries with remaining validity |
| [`lint`](#lint) | Check integration flows for reuse, dead weight, Partner Directory candidates and best practices (local files) |
| [`locks`](#locks) | List the edit locks of artifacts, remove one (unlock) |
| [`locks list`](#locks-list) | List edit locks (who, since when) |
| [`locks unlock`](#locks-unlock) | Remove the edit lock of an artifact (requires --confirm) |
| [`log-files`](#log-files) | List system and HTTP log files of the runtime |
| [`log-files get`](#log-files-get) | Print the end of a log file |
| [`log-level`](#log-level) | Set the message processing log level of a deployed integration flow |
| [`logs`](#logs) | Query message processing logs |
| [`logs attachment`](#logs-attachment) | Download a log attachment (ID from 'logs get') |
| [`logs get`](#logs-get) | Show one message: status, error text, custom headers, attachments |
| [`logs payload`](#logs-payload) | Download a persisted message (message store entry ID from 'logs get') |
| [`logs steps`](#logs-steps) | Show the processing steps of a message and the step that failed |
| [`logs summary`](#logs-summary) | Message volume and failures per flow, per connection between flows and per error fingerprint |
| [`logs trace`](#logs-trace) | List the traced steps of a message (flow on log level TRACE) |
| [`logs trace-message`](#logs-trace-message) | Show payload, headers and exchange properties of a traced step (ID from 'logs trace') |
| [`logs tree`](#logs-tree) | Show the path of a message or trace across flows and its first failure |
| [`matrix`](#matrix) | Version matrix: every artifact's version in Git and on each tier, and what is ready to promote |
| [`mcp`](#mcp) | Run the MCP server (stdio) for AI agents |
| [`mock-tenant`](#mock-tenant) | Run an in-memory SAP CPI tenant for local development and tests (never a real tenant) |
| [`number-ranges`](#number-ranges) | List number ranges |
| [`orchestrator`](#orchestrator) | Update and deploy many packages from a local directory tree |
| [`packages`](#packages) | List integration packages |
| [`packages create`](#packages-create) | Create an integration package if it does not exist |
| [`params`](#params) | Read or change externalised parameters of an integration flow |
| [`params get`](#params-get) | Show the parameters of an integration flow |
| [`params set`](#params-set) | Set parameters of an integration flow (deploy afterwards to activate) |
| [`pd`](#pd) | Inspect Partner Directory parameters (get, diff, deps) |
| [`pd deps`](#pd-deps) | Show which flows read which Partner Directory parameters (local files) |
| [`pd diff`](#pd-diff) | Compare local Partner Directory files with the tenant |
| [`pd get`](#pd-get) | Show the Partner Directory parameters of a partner ID on the tenant |
| [`pd-deploy`](#pd-deploy) | Upload Partner Directory parameters from local files |
| [`pd-snapshot`](#pd-snapshot) | Download Partner Directory parameters into local files |
| [`profile`](#profile) | Switch between tenants (profiles in $HOME/.cpictl) |
| [`profile current`](#profile-current) | Show the profile that commands use now |
| [`profile list`](#profile-list) | List profiles and mark the active one |
| [`profile use`](#profile-use) | Make a profile the default (stored in $HOME/.cpictl/current); 'use -' clears it |
| [`resources`](#resources) | List the resources (scripts, mappings, schemas, ...) of an integration flow |
| [`resources get`](#resources-get) | Download one resource of an integration flow |
| [`send`](#send) | Send a test message to a deployed integration flow |
| [`skills`](#skills) | List, read and install the cpi skills built into cpictl |
| [`skills install`](#skills-install) | Copy the skills into the skill folder of an agent |
| [`skills list`](#skills-list) | List the skills and what they are for |
| [`skills show`](#skills-show) | Print a skill's SKILL.md or one of its reference files |
| [`snapshot`](#snapshot) | Save all integration packages of the tenant to a Git repository |
| [`snapshot restore`](#snapshot-restore) | Create or update integration packages on the tenant from a Git repository |
| [`stats`](#stats) | Show local usage statistics (which commands and MCP tools run, how often, how long) |
| [`status`](#status) | Show runtime status, version and errors of artifacts |
| [`sync`](#sync) | Synchronise the artifacts of a package between tenant and Git |
| [`sync apiproduct`](#sync-apiproduct) | Synchronise API Management products between tenant and Git |
| [`sync apiproxy`](#sync-apiproxy) | Synchronise API Management proxies (with dependent artifacts) between tenant and Git |
| [`transport`](#transport) | Prepare a transport between tiers: dependencies, pre-checks against the target, copy artifact folders |
| [`transport check`](#transport-check) | Check a transport against the target tenant (read only) |
| [`transport copy`](#transport-copy) | Copy artifact folders into another content tree (local files) |
| [`transport deps`](#transport-deps) | List what the selected artifacts depend on (local files) |
| [`undeploy`](#undeploy) | Remove artifacts from runtime and wait until they are gone |
| [`update`](#update) | Create or update designtime artifacts and packages |
| [`update artifact`](#update-artifact) | Create or update a designtime artifact from a local directory |
| [`update package`](#update-package) | Create or update an integration package from a JSON file |
| [`validate`](#validate) | Validate an integration flow on the tenant (like Check in the Web UI) |
| [`variables`](#variables) | List global and integration flow variables |
| [`variables get`](#variables-get) | Read the value of a variable |
| [`version`](#version) | Artifact versions in the repository (Bundle-Version) |
| [`version bump`](#version-bump) | Raise Bundle-Version of (changed) artifacts |

## Global flags

```
      --config string               config file (default: the profile, else $CPICTL_CONFIG, else $HOME/cpictl.yaml plus ./cpictl.yaml of the repository)
      --debug                       Show debug logs
      --oauth-clientid string       Client ID for using OAuth
      --oauth-clientsecret string   Client Secret for using OAuth
      --oauth-host string           OAuth token server host
      --oauth-path string           Path for OAuth token server (default "/oauth/token")
      --output string               Output format: text or json. With json the result is written to stdout as one JSON document and logs are written to stderr as JSON lines (default "text")
      --profile string              Profile to use: $HOME/.cpictl/<name>.yaml (default: $CPICTL_PROFILE, else the one chosen with 'cpictl profile use')
      --read-retries int            Retries of a tenant read (GET) answered with 429, 502, 503 or 504, with backoff 2s, 4s, 8s ... (0: none). Writes are never retried (default 3)
      --summary string              Append a markdown job summary of orchestrator, configure, deploy, undeploy and snapshot to this file (default: $GITHUB_STEP_SUMMARY when set; "off": none)
      --tmn-host string             Tenant host of Cloud Integration (or API portal host for API Management)
      --tmn-password string         Password for Basic Auth
      --tmn-userid string           User ID for Basic Auth
```

## artifacts

List designtime artifacts of a package

**Usage:** `cpictl artifacts [flags]`

**Flags:**

```
      --package-id string   Integration package ID
```

## compare

Compare two tiers, Git refs or content trees per artifact (content, version, parameters, files)

```
Compare two sides per artifact. A side is:

  <directory>          a content tree (<package>/<artifact>, as snapshot writes it)
  git:<ref>[:<path>]   a Git ref of the repository (--repo), e.g. git:main:packages
  tenant               the configured tenant (--profile, config, environment)
  tenant:<profile>     the tenant of a profile (~/.cpictl/<profile>.yaml)

Tenants are only read: their artifacts are downloaded and normalized as snapshot
writes them, so only real differences show. Per artifact the status is same,
only_a, only_b, content_differs (as an upload compares: without Bundle-Version and
parameters.prop), version_differs (same content) or parameters_differ (same content
and version); per file added / removed / changed (--diff: unified diffs); per
parameters.prop key differs / only_a / only_b (--show-values: the values). For a
tenant side the designtime and running versions, the draft flag and, where the
tenant reports it, who changed the artifact last.
```

**Usage:** `cpictl compare <side A> <side B> [flags]`

**Flags:**

```
      --all                Also list artifacts that are the same (text output)
      --artifact strings   Only these artifacts (IDs or patterns)
      --diff               Unified diffs of changed text files
      --fail-on-diff       Exit with code 5 when any artifact differs
      --package strings    Only these packages (IDs or patterns)
      --parallel int       Downloads at the same time per tenant (config: compare.parallel) (default 8)
      --repo string        Git repository for git: sides (default ".")
      --show-values        Parameter values (default: only which keys differ)
```

**Examples:**

```
  cpictl compare tenant:test tenant:prod --package Orders
  cpictl compare packages tenant --diff                 # repository vs tenant (drift)
  cpictl compare git:release/2026-10:packages git:main:packages
  cpictl compare tenant:dev tenant:test --fail-on-diff --output json
```

## components

Component catalogue: the adapter and step versions the tenant offers

```
The component catalogue (.cpi/components.json) lists the adapter and step
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
Entries from the editor metadata win over learned ones.
```

**Examples:**

```
  cpictl components import metadata.json
  cpictl components learn                 # all flows of the tenant
  cpictl components learn --dir ./content # local flows, offline
  cpictl components list --name HTTP
```

## components import

Build the catalogue from the Web UI's editor metadata

**Usage:** `cpictl components import <editor-metadata.json|-> [flags]`

**Flags:**

```
      --tenant string   Name of the tenant the metadata is from (e.g. the profile or tier)
```

## components learn

Add the variants existing flows use (tenant, or --dir offline)

**Usage:** `cpictl components learn [flags]`

**Flags:**

```
      --dir string            Learn from this local content tree instead of the tenant
      --package-ids strings   Only these packages (default: all)
```

## components list

List the catalogue (filter by name, kind, direction)

**Usage:** `cpictl components list [flags]`

**Flags:**

```
      --direction string   Sender or Receiver (adapters)
      --kind string        adapter or step
      --name string        Only variants whose name contains this (case-insensitive)
```

## config-generate

Generate or refresh the orchestrator deployment config from a packages directory

```
Generate or update deployment configuration from package directory structure.

This command scans the packages directory and generates/updates a deployment configuration
file (001-deploy-config.yml) with all discovered packages and artifacts.

Features:
  - Extracts package metadata from {PackageName}.json files
  - Extracts artifact display names from MANIFEST.MF (Bundle-Name)
  - Extracts artifact types from MANIFEST.MF (SAP-BundleType)
  - Preserves existing configuration settings (sync/deploy flags, config overrides)
  - Smart merging of new and existing configurations
  - Filter by specific packages or artifacts
```

**Usage:** `cpictl config-generate [flags]`

**Flags:**

```
      --artifact-filter strings   Comma separated list of artifacts to include (e.g., 'Artifact1,Artifact2')
      --output-file string        Path to output configuration file (default "./001-deploy-config.yml")
      --package-filter strings    Comma separated list of packages to include (e.g., 'Package1,Package2')
      --packages-dir string       Path to packages directory (default "./packages")
```

**Examples:**

```
  # Generate config with defaults
  cpictl config-generate

  # Specify custom directories
  cpictl config-generate --packages-dir ./my-packages --output-file ./my-config.yml

  # Generate config for specific packages only
  cpictl config-generate --package-filter "DeviceManagement,GenericPipeline"

  # Generate config for specific artifacts only
  cpictl config-generate --artifact-filter "MDMEquipmentMutationOutbound,GenericBroadcaster"

  # Combine package and artifact filters
  cpictl config-generate --package-filter "DeviceManagement" --artifact-filter "MDMEquipmentMutationOutbound"
```

## configure

Set artifact parameters from YAML files and optionally deploy

```
Set externalised parameters of many artifacts from YAML files and optionally
deploy them afterwards.

Phase 1 compares each artifact's parameters with the tenant and writes only the
ones that differ (OData $batch by default, falling back to single requests); an
unknown key fails the artifact before anything is written. Phase 2 deploys the
artifacts marked with deploy: true that had a change, package by package with up
to --parallel-deployments concurrent deployments. --force writes and deploys
everything as before.

--config-path accepts a file or a folder (all *.yml/*.yaml files, not
recursive). Generate files with the current tenant values with
'cpictl configure pull'. File format: docs/configure.md.

All flags can be set in the config file under 'configure'.
```

**Usage:** `cpictl configure [flags]`

**Flags:**

```
      --allow-downgrade            Deploy designtime versions older than the running ones, for artifacts and packages without allowDowngrade in the file (config: configure.allowDowngrade, else deploy.allowDowngrade)
      --artifact-filter string     Comma-separated list of artifacts to include (config: configure.artifactFilter)
      --batch-size int             Parameters per batch request (config: configure.batchSize) (default 90)
  -c, --config-path string         Path to configuration YAML file (config: configure.configPath)
      --defer-deploy               Do not deploy: add the deployments to the pending file for one 'cpictl deploy --pending' at the end
      --deploy-delay int           Seconds between deployment status checks (config: configure.deployDelaySeconds) (default 10)
      --deploy-retries int         Deployment status checks per artifact (config: configure.deployRetries) (default 30)
  -p, --deployment-prefix string   Deployment prefix for artifact IDs (config: configure.deploymentPrefix)
      --disable-batch              Disable batch processing, use individual requests (config: configure.disableBatch)
      --dry-run                    Show what would be done without making changes, including which artifacts would be deployed and why (config: configure.dryRun)
      --force                      Write all parameters and deploy all marked artifacts, even if the tenant already has the values
      --offline                    With --dry-run: only show the file contents, do not read the tenant
      --package-filter string      Comma-separated list of packages to include (config: configure.packageFilter)
      --parallel int               Artifacts whose parameters are read at the same time (config: configure.parallel) (default 8)
      --parallel-deployments int   Deployments at the same time per package (config: configure.parallelDeployments) (default 5)
      --pending-file string        Pending deployments file (default: .cpi/pending-deploy.json)
      --plan                       Same as --dry-run
      --versioning string          Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

**Examples:**

```
  # Configure artifacts from a config file
  cpictl configure --config-path ./config/dev-config.yml

  # Configure and deploy
  cpictl configure --config-path ./config/prod-config.yml

  # Dry run: compare with the tenant (what would be written and deployed)
  cpictl configure --config-path ./config.yml --dry-run

  # Only show the file, without reading the tenant
  cpictl configure --config-path ./config.yml --dry-run --offline

  # Write everything and redeploy, even if the tenant has the values
  cpictl configure --config-path ./config.yml --force

  # Apply deployment prefix
  cpictl configure --config-path ./config.yml --deployment-prefix DEV_

  # Disable batch processing
  cpictl configure --config-path ./config.yml --disable-batch
```

## configure pull

Write current tenant parameter values into configure YAML files

**Usage:** `cpictl configure pull [flags]`

**Flags:**

```
  -o, --output-dir string     Directory for one YAML file per package (default ".")
      --package-ids strings   Package IDs to pull (default: all packages)
      --parallel int          Tenant reads at the same time (config: configure.pull.parallel) (default 8)
```

## credentials

List and deploy user credentials, OAuth2 client credentials and secure parameters

```
Manage security material referenced by integration flows.

Secrets are never accepted as flag values (they would end up in shell history
and process lists): use --*-env, --*-file or --*-stdin, or 'credentials apply'
with a YAML file that references environment variables or files.
Listing never returns secrets.
```

## credentials apply

Create or update all credentials of a YAML file

```
Deploy every credential listed in a YAML file. Secrets are references only:

  userCredentials:
    - name: ERP_User
      user: svc_erp
      password: {env: ERP_PASSWORD}
  oauth2Credentials:
    - name: Graph
      tokenServiceUrl: https://login.example.com/oauth/token
      clientId: my-app
      clientSecret: {file: secrets/graph.txt}   # relative to the YAML file
  secureParameters:
    - name: ApiKey
      value: {env: API_KEY}

All secrets are resolved before anything is written. Inline secrets and
unknown keys are rejected.
```

**Usage:** `cpictl credentials apply [flags]`

**Flags:**

```
      --dry-run       Resolve all secrets and validate, without writing
      --file string   Credentials YAML file
```

## credentials delete

Delete a credential (requires --confirm)

**Usage:** `cpictl credentials delete [flags]`

**Flags:**

```
      --confirm       Confirm the deletion
      --kind string   Kind: user, oauth2, secure-param
      --name string   Credential name
```

## credentials list

List credentials (names and metadata, never secrets)

**Usage:** `cpictl credentials list [flags]`

**Flags:**

```
      --kind string   Only this kind: user, oauth2, secure-param
```

## credentials set-oauth2

Create or update an OAuth2 client credential

**Usage:** `cpictl credentials set-oauth2 [flags]`

**Flags:**

```
      --audience string             Audience
      --client-auth string          Client authentication: body or header (default "body")
      --client-id string            Client ID
      --description string          Description
      --dry-run                     Check the input without writing
      --name string                 Credential name
      --resource string             Resource
      --scope string                Scope
      --scope-content-type string   urlencoded or json (default "urlencoded")
      --secret-env string           Read the client secret from this environment variable
      --secret-file string          Read the client secret from this file
      --secret-stdin                Read the client secret from stdin
      --token-url string            Token service URL (https)
```

**Examples:**

```
  cpictl credentials set-oauth2 --name Graph --token-url https://login/token --client-id app --secret-file ./graph.secret
```

## credentials set-secure-param

Create or update a secure parameter (Neo environment)

**Usage:** `cpictl credentials set-secure-param [flags]`

**Flags:**

```
      --description string   Description
      --dry-run              Check the input without writing
      --name string          Parameter name
      --value-env string     Read the value from this environment variable
      --value-file string    Read the value from this file
      --value-stdin          Read the value from stdin
```

## credentials set-user

Create or update a user credential

**Usage:** `cpictl credentials set-user [flags]`

**Flags:**

```
      --company-id string      Company ID (SuccessFactors)
      --description string     Description
      --dry-run                Check the input without writing
      --kind string            default, successfactors or openconnectors (default "default")
      --name string            Credential name (as referenced in the iFlow)
      --password-env string    Read the password from this environment variable
      --password-file string   Read the password from this file
      --password-stdin         Read the password from stdin
      --user string            User name
```

**Examples:**

```
  ERP_PASSWORD=... cpictl credentials set-user --name ERP_User --user svc_erp --password-env ERP_PASSWORD
```

## datastore

List data stores and their entries, read or delete an entry

**Examples:**

```
  cpictl datastore list --overdue-only
  cpictl datastore entries --data-store Orders --artifact-id OrderIntake
  cpictl datastore get --data-store Orders --artifact-id OrderIntake --id e1 --out entry.xml
  cpictl datastore delete --data-store Orders --artifact-id OrderIntake --id e1 --confirm
```

## datastore delete

Delete a data store entry (requires --confirm)

**Usage:** `cpictl datastore delete [flags]`

**Flags:**

```
      --artifact-id string   Integration flow of the data store (empty for global stores)
      --confirm              Confirm the deletion
      --data-store string    Data store name
      --id string            Entry ID
      --type string          Data store type (stores of adapters or steps, e.g. XI, AS4)
```

## datastore entries

List entries of a data store (or of all stores)

**Usage:** `cpictl datastore entries [flags]`

**Flags:**

```
      --artifact-id string    Integration flow of the data store (empty for global stores)
      --data-store string     Data store name
      --message-guid string   Only entries written by this message
      --overdue-only          Only overdue entries
      --top int               Maximum entries (default 100)
      --type string           Data store type (stores of adapters or steps, e.g. XI, AS4)
```

## datastore get

Download the content of a data store entry

**Usage:** `cpictl datastore get [flags]`

**Flags:**

```
      --artifact-id string   Integration flow of the data store (empty for global stores)
      --data-store string    Data store name
      --id string            Entry ID
      --max-bytes int        Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --out string           Write the content to this file instead of stdout / the JSON result
      --type string          Data store type (stores of adapters or steps, e.g. XI, AS4)
```

## datastore list

List data stores with their number of entries

**Usage:** `cpictl datastore list [flags]`

**Flags:**

```
      --overdue-only   Only stores with overdue entries
```

## deploy

Deploy designtime artifacts and wait for the result

```
Deploy artifact from designtime to
runtime of SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'deploy' section. CLI flags override config file settings.
```

**Usage:** `cpictl deploy [flags]`

**Flags:**

```
      --allow-downgrade            Deploy even if the designtime version is older than the running version (config: deploy.allowDowngrade)
      --artifact-ids strings       Comma separated list of artifact IDs (config: deploy.artifactIds)
      --artifact-type string       Artifact type. Allowed values: Integration, MessageMapping, ScriptCollection, ValueMapping (config: deploy.artifactType) (default "Integration")
      --compare-versions           Perform version comparison of design time against runtime before deployment (config: deploy.compareVersions) (default true)
      --delay-length int           Seconds between deployment status checks (config: deploy.delayLength) (default 10)
      --max-check-limit int        Deployment status checks per artifact (config: deploy.maxCheckLimit) (default 30)
      --parallel-deployments int   With --pending: deployments at the same time per package (config: deploy.parallelDeployments) (default 5)
      --pending                    Deploy the pending deployments that orchestrator and configure --defer-deploy collected (each artifact once); failed ones stay in the file
      --pending-file string        Pending deployments file (default: .cpi/pending-deploy.json)
      --plan                       Only report per artifact whether it would be deployed and why; nothing is triggered
      --versioning string          Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

## discover

Inventory existing integration flows to derive conventions

```
Inventory the packages and integration flows of the tenant (or of a local
directory with --dir) and write the facts as JSON: adapters, steps, exception
subprocesses, log levels, scripts (identical scripts across flows), externalised
parameter keys, credential names, headers and properties that are set, and
naming patterns of packages and flows.

The tenant is only read; every integration flow is downloaded and analysed in
memory. The file is the input for writing a repository's conventions (see the
cpi-discover skill).

Next to it, graph.json holds the same content as a graph for fast lookups (see
'cpictl graph'): which flows call which through ProcessDirect and JMS addresses,
which flows share credentials, scripts, Partner Directory parameters and headers,
and which systems they call. --graph=false skips it.
```

**Usage:** `cpictl discover [flags]`

**Flags:**

```
      --components string     Add the variants the flows use to this component catalogue (default: components.json next to the output file; -: skip; see 'cpictl components')
      --dir string            Analyse this local directory instead of the tenant
      --graph                 Also write graph.json next to the output file (default true)
      --max-iflows int        Stop after this many integration flows (0: all)
      --output-file string    JSON file to write (default ".cpi/discovery.json")
      --package-ids strings   Only these packages (default: all)
```

**Examples:**

```
  cpictl discover --output-file .cpi/discovery.json
  cpictl discover --package-ids SalesOrders,Finance
  cpictl discover --dir ./content   # local repository from 'sync' or 'snapshot', offline
```

## doctor

Check the setup: configuration, connection and which API areas the credentials can use

```
Check the setup and report what works:

  local   config file / profile, tenant host, authentication method, runtime
          credentials for test messages, git, snapshot state and pending
          deployments in .cpi, usage statistics
  tenant  connection and authentication, then one small read (GET, $top=1) per
          API area: designtime, runtime, message logs, security material,
          keystore, Partner Directory, data stores, log files. 403 means the
          credentials lack the role for that area, 404 that the tenant does not
          offer the API.

Nothing is changed. Exit code 3 when authentication fails, 4 without a
connection, 2 without a tenant host; otherwise 0, also when optional areas are
forbidden (see the result).
```

**Usage:** `cpictl doctor`

## download

Download a designtime artifact and extract it into a directory

**Usage:** `cpictl download [flags]`

**Flags:**

```
      --artifact-id string     Artifact ID
      --artifact-type string   Artifact type: Integration, MessageMapping, ScriptCollection, ValueMapping (default "Integration")
      --dir string             Target directory (must be empty unless --overwrite)
      --overwrite              Replace the content of a non-empty target directory
      --version string         Designtime version (default "active")
```

**Examples:**

```
  cpictl download --artifact-id OrderIntake --dir ./OrderIntake
  cpictl download --artifact-id OrderMapping --artifact-type MessageMapping --dir ./OrderMapping --overwrite
```

## drift

Compare local artifacts with their designtime and runtime state on the tenant

```
For each artifact of a local content tree: the local Bundle-Version and content
against the designtime version and content on the tenant (compared like upload
does), and the deployed version.

  in_sync        same content
  tenant_newer   content differs and the tenant has the higher version
                 (edited on the tenant: download before uploading)
  local_newer    content differs and the local version is higher
  diverged       content differs with the same version
  not_on_tenant  the artifact does not exist on the tenant

runtimeOutdated marks artifacts whose deployed version differs from the
designtime version. The tenant is only read; every artifact is downloaded.
```

**Usage:** `cpictl drift [flags]`

**Flags:**

```
      --local-dir string    Local content directory (default ".")
      --package-id string   Only artifacts in this package folder
      --parallel int        Artifacts compared at the same time (config: drift.parallel) (default 8)
```

**Examples:**

```
  cpictl drift --local-dir ./content --package-id Orders
```

## endpoints

List the URLs of deployed integration flows

**Usage:** `cpictl endpoints [flags]`

**Flags:**

```
      --artifact-id string   Only endpoints of this integration flow
```

**Examples:**

```
  cpictl endpoints --artifact-id OrderIntake
```

## graph

Query the content graph written by discover (offline)

```
Query .cpi/graph.json, the graph that 'cpictl discover' writes next to
discovery.json. Nodes: package, iflow, endpoint, system, credential, script, header, property, customheader, pd.
Node IDs are <type>:<key> (iflow:Orders_In, endpoint:ProcessDirect:/billing/in,
pd:SAP_SYSTEM_001:Identity); commands also accept a key or a unique name.
Edges: contains, exposes, calls, sends_to, calls_system, uses_credential, uses_script, sets_header, sets_property, logs_header, reads_pd.
sends_to links flows through matching ProcessDirect and JMS addresses;
{{parameter}} addresses are resolved with the flow's parameters.prop value.

The graph shows what discovery saw: rerun discover after changes. Without
graph.json, discovery.json in the same folder is used.
```

**Examples:**

```
  cpictl graph search billing
  cpictl graph neighbors Billing --direction in --edge-types sends_to
  cpictl graph neighbors credential:SFTP_User
  cpictl graph path Orders_In Invoice_Send
```

## graph build

Write graph.json from an existing discovery.json

```
Build the graph from a discovery file without discovering again. Discovery
files written before cpictl had the graph lack receiver addresses and Partner
Directory references: run discover again for the full graph.
```

**Usage:** `cpictl graph build [flags]`

**Flags:**

```
      --discovery-file string   Discovery file to read (default ".cpi/discovery.json")
```

**Examples:**

```
  cpictl graph build --discovery-file .cpi/discovery.json
```

## graph neighbors

Show what a node is connected to

**Usage:** `cpictl graph neighbors NODE [flags]`

**Flags:**

```
      --depth int            Hops (1 to 3) (default 1)
      --direction string     out (what the node uses or calls), in (what uses or calls it), both (default "both")
      --edge-types strings   Only these edge types
      --limit int            Maximum number of edges (default 200)
```

## graph path

Find the shortest connection between two nodes

```
Find the shortest connection between two nodes, following edges in both
directions. By default only exposes, calls and sends_to are followed (how
messages move between flows); --edge-types widens it, e.g. uses_credential.
```

**Usage:** `cpictl graph path FROM TO [flags]`

**Flags:**

```
      --edge-types strings   Edge types to follow (default: exposes, calls, sends_to)
      --max-depth int        Maximum number of hops (default 6)
```

## graph search

Find nodes by key, name or attribute

**Usage:** `cpictl graph search QUERY [flags]`

**Flags:**

```
      --limit int       Maximum number of matches (default 50)
      --types strings   Only these node types
```

## guidelines

Check an integration flow against the design guidelines activated on the tenant

```
Run the design guidelines that the tenant administrator activated against an
integration flow and wait for the result. Violations (not compliant, not
skipped) give exit code 5.
```

**Usage:** `cpictl guidelines [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --timeout duration     Maximum time to wait for the result (default 2m0s)
      --version string       Designtime version (default "active")
```

**Examples:**

```
  cpictl guidelines --artifact-id OrderIntake --output json
```

## id-mappings

Show ID mapper entries of a source or target ID

**Usage:** `cpictl id-mappings [flags]`

**Flags:**

```
      --source-id string   Source ID
      --target-id string   Target ID
```

## idempotent

List idempotent repository entries (messages or files skipped as duplicates)

**Usage:** `cpictl idempotent [flags]`

**Flags:**

```
      --component string   Only this component, e.g. SFTP or XI
      --id string          Entry ID (SFTP: <directory>/<file name>, XI: message ID)
      --source string      Only sources containing this text
```

**Examples:**

```
  cpictl idempotent --id in/orders_20261005.csv --component SFTP
```

## iflow

Work with integration flow files (copy a template under a new ID, lay out the diagram)

## iflow copy

Copy an integration flow under a new ID, name and sender address

```
Copy an integration flow (a template or any reference flow) into a local
directory and rename it, so that it can be uploaded as a new flow:

  META-INF/MANIFEST.MF  Bundle-SymbolicName (";singleton:=true" kept), Bundle-Name,
                        Bundle-Version (default 1.0.0); long values wrapped at 72 bytes
  metainfo.prop         description (the source's description is removed when
                        --description is not given)
  <SourceID>.iflw       renamed to <ID>.iflw (other model file names are kept)
  .project              project name
  sender addresses      HTTPS/SOAP path, ProcessDirect address, SFTP directory,
                        JMS queue, ...: in the model, or in parameters.prop when the
                        address is a {{parameter}}

Every sender address must get a new value (--address), or --keep-addresses must
be given: two deployed flows on the same HTTP path or ProcessDirect address fail
to deploy, and two flows polling the same directory or queue take each other's
messages. One --address NEW is enough when the flow has one sender address;
otherwise give --address OLD=NEW per address (OLD as in the source, e.g.
{{Orders_Path}}). A value containing "=" is always read as OLD=NEW.

Receivers, steps, scripts and parameters other than the addresses are copied
unchanged. The result lists files that still contain the source ID (process
names, scripts, log texts): check them. The source is read from the tenant
(--from, read only) or a local directory (--from-dir, offline). Nothing is
written to the tenant: upload the copy with 'cpictl update artifact'. On an
error the target directory is left as it was.
```

**Usage:** `cpictl iflow copy [flags]`

**Flags:**

```
      --address stringArray   New sender address: NEW (one sender) or OLD=NEW (repeatable)
      --description string    Description of the new flow
      --dir string            Target directory, must not exist or be empty (default: <ID> next to --from-dir, or ./<ID>)
      --from string           ID of the source flow on the tenant
      --from-dir string       Local directory of the source flow (instead of --from)
      --from-version string   Version of the source flow on the tenant (default "active")
      --id string             ID of the new flow
      --keep-addresses        Allow sender addresses that stay the same as in the source
      --name string           Display name of the new flow (default: the ID)
      --version string        Version of the new flow (default "1.0.0")
```

**Examples:**

```
  # template on the tenant, one HTTPS sender
  cpictl iflow copy --from Template_Sync_HTTPS --id SD_Orders_S4_Sync \
    --name "SD Orders to S/4 (sync)" --description "Order intake from the web shop" \
    --address /sd/orders/s4 --dir content/SDOrders/SD_Orders_S4_Sync

  # local template with two senders (a parameterised HTTPS path and a ProcessDirect test entry)
  cpictl iflow copy --from-dir content/Templates/Template_Async --id FI_Invoices_In \
    --address '{{Inbound_Path}}=/fi/invoices' --address /test/Template_Async=/test/FI_Invoices_In

  # then
  cpictl update artifact --artifact-id SD_Orders_S4_Sync --package-id SDOrders \
    --dir-artifact content/SDOrders/SD_Orders_S4_Sync
```

## iflow layout

Lay out the diagram of integration flows: steps in flow order, no overlaps, right-angled lines (local files)

```
Recompute the diagram (BPMNDiagram) of .iflw files: steps left to right in flow
order, branches one below the other, exception subprocesses below the main flow,
senders left and receivers right of the integration process, level with the steps
they talk to, and right-angled lines that bend between columns. Only the diagram
changes, never the steps, their configuration or the sequence flows; a second run
changes nothing.

Paths are .iflw files or directories (searched recursively: an artifact folder, a
package, the whole content tree).

  --mode tidy   keeps the order of steps and branches (default)
  --mode full   also reorders branches to reduce crossing lines
  --check       only reports problems (missing shapes, overlaps, shapes outside the
                pool, lines through steps, cramped shapes); exit code 5 when any

Spacing and mode can be set in .cpi/lint.yaml (layout: {mode, hgap, vgap}).
Review the result in the Web UI before deploying.
```

**Usage:** `cpictl iflow layout <path>... [flags]`

**Flags:**

```
      --check          Only report layout problems; exit code 5 when any
      --dry-run        Compute the layout but write nothing
      --hgap float     Space between columns of steps (config: .cpi/lint.yaml layout.hgap) (default 60)
      --mode string    tidy (keep the order of steps and branches) or full (also reorder branches) (config: .cpi/lint.yaml layout.mode) (default "tidy")
      --rules string   Lint config with the layout settings (config: lint.rules) (default ".cpi/lint.yaml")
      --vgap float     Space between rows (config: .cpi/lint.yaml layout.vgap) (default 40)
```

**Examples:**

```
  cpictl iflow layout packages/Orders/OrderIntake
  cpictl iflow layout packages --check
  cpictl iflow layout packages/Orders --mode full --dry-run
```

## jms

JMS queues and broker capacity

## jms broker

Show JMS broker capacity and usage

**Usage:** `cpictl jms broker`

## jms queues

List JMS queues, fullest first

**Usage:** `cpictl jms queues [flags]`

**Flags:**

```
      --prefix string   Only queues whose name starts with this
```

## keystore

List keystore entries, check expiry, export and import certificates

## keystore export-cert

Export the certificate of a keystore entry as PEM

**Usage:** `cpictl keystore export-cert [flags]`

**Flags:**

```
      --alias string      Keystore alias
      --keystore string   Keystore: system, backup_admin_system, KeyRenewal, KeyHistory (default "system")
      --out string        Write the PEM to this file instead of stdout
```

## keystore import-cert

Import a certificate (PEM or DER) into the tenant keystore

**Usage:** `cpictl keystore import-cert [flags]`

**Flags:**

```
      --alias string   Keystore alias
      --file string    Certificate file (PEM or DER)
      --update         Replace an existing entry with the same alias
```

**Examples:**

```
  cpictl keystore import-cert --alias partner_acme --file acme.pem
```

## keystore list

List keystore entries with remaining validity

**Usage:** `cpictl keystore list [flags]`

**Flags:**

```
      --expiring-within string   Flag entries expiring within this period (e.g. 30d, 720h)
      --fail-on-expiry           Exit with code 5 if an entry is expired or expiring
      --keystore string          Keystore: system, backup_admin_system, KeyRenewal, KeyHistory (default "system")
```

**Examples:**

```
  cpictl keystore list --expiring-within 30d
  cpictl keystore list --expiring-within 30d --fail-on-expiry   # exit 5 in CI
```

## lint

Check integration flows for reuse, dead weight, Partner Directory candidates and best practices (local files)

```
Check the integration flows of a content tree (<package>/<artifact>, as snapshot
writes it) and report findings with a rule, a severity and a suggestion:

  reuse              scripts in several flows (-> script collection), mappings in
                     several flows, missing script collections
  partner-directory  routers on many literal values, lookup tables in scripts,
                     flows deployed several times with different configOverrides
  dead-weight        unconnected steps, unused scripts, resources and parameters,
                     content modifiers that do nothing, properties nobody reads
  simplify           content modifiers in a row, XML/JSON round trips, scripts
                     that only set headers, very long scripts
  robustness         no exception subprocess, swallowed exceptions
  performance        whole body as string, payload attachments, logging in loops
  configuration      fixed receiver addresses, URLs and secrets in scripts
  hygiene            outdated step versions, default step names, naming rule

Only local files are read; all flows are read for the cross-flow rules, the
filters select which flows are reported. Rules, severities and thresholds:
.cpi/lint.yaml (--rules; cpictl lint --list-rules). Known findings can be recorded in a
baseline so that --fail-on fails only on new ones.

--fix applies the mechanical fixes to the local files: scripts move into script
collections (the package's, or a shared one with scriptCollections.crossPackage),
unused scripts are deleted, unconnected steps removed (--fix-rules all also
removes content modifiers that do nothing). Review the diff, raise the versions
(version bump --changed) and deploy the collections before the flows.
```

**Usage:** `cpictl lint [flags]`

**Flags:**

```
      --artifact strings       Only report these artifacts (names or patterns)
      --baseline string        Known findings; --fail-on counts only new ones (config: lint.baseline) (default ".cpi/lint-baseline.json")
      --changed                Only report artifacts changed in Git since --since (committed, uncommitted, untracked)
      --components string      Component catalogue of the target tenant, used when it exists (config: lint.components) (default ".cpi/components.json")
      --deploy-config string   Deploy config for the deployment-copies rule (default: orchestrator.deployConfig)
      --dir string             Content tree (default: packages if it exists, else the current directory) (config: lint.dir)
      --dry-run                With --fix: only list the changes
      --fail-on string         Exit with code 5 when a new finding has at least this severity: info, warning, error (config: lint.failOn)
      --fix                    Apply the fixes to the local files
      --fix-rules strings      Rules to fix (default: duplicate-script, use-script-collection, unused-script, unconnected-step; all: every fixable rule)
      --list-rules             List the rules
      --min-severity string    Do not report findings below this severity (default "info")
      --package strings        Only report these packages (names or patterns)
      --rules string           Rules file: severities, thresholds, script collection names (config: lint.rules) (default ".cpi/lint.yaml")
      --since string           Git ref for --changed (e.g. origin/main in a pull request) (default "HEAD")
      --update-baseline        Record the current findings as known
```

**Examples:**

```
  cpictl lint
  cpictl lint --package UtilitiesBaseEDM --output json
  cpictl lint --changed --since origin/main --fail-on warning
  cpictl lint --update-baseline
  cpictl lint --fix --dry-run
  cpictl lint --fix --package UtilitiesBaseEDM
```

## locks

List the edit locks of artifacts, remove one (unlock)

```
An artifact opened for editing in the Web UI is locked for everyone else until the
editor saves or cancels. A browser closed while editing leaves the lock behind.
unlock removes it, as "Unlock" in the Web UI does: whoever held the lock loses
unsaved changes.
```

**Examples:**

```
  cpictl locks list
  cpictl locks list --package-id Orders
  cpictl locks unlock --artifact-id Orders_In --confirm
```

## locks list

List edit locks (who, since when)

**Usage:** `cpictl locks list [flags]`

**Flags:**

```
      --artifact-id string   Only this artifact's lock
      --package-id string    Only locks in this package
```

## locks unlock

Remove the edit lock of an artifact (requires --confirm)

**Usage:** `cpictl locks unlock [flags]`

**Flags:**

```
      --artifact-id string   Artifact to unlock
      --confirm              Confirm: the editor holding the lock loses unsaved changes
```

## log-files

List system and HTTP log files of the runtime

**Usage:** `cpictl log-files [flags]`

**Flags:**

```
      --since string   Only files modified after this time (duration like 2h or RFC 3339)
      --type string    Log file type, e.g. http or trace
```

**Examples:**

```
  cpictl log-files --type http --since 2h
  cpictl log-files get --name http_access_2026-10-05.log --application it-cpi --tail-bytes 20000
```

## log-files get

Print the end of a log file

**Usage:** `cpictl log-files get [flags]`

**Flags:**

```
      --application string   Application of the log file
      --name string          Log file name
      --out string           Write the content to this file instead of stdout / the JSON result
      --tail-bytes int       Bytes from the end of the file (default 65536)
```

## log-level

Set the message processing log level of a deployed integration flow

```
Set the log level of a deployed integration flow: NONE, INFO, DEBUG or TRACE.
TRACE records payload and headers at every step for 10 minutes, then the tenant
falls back to the previous level; read traces with 'logs trace'. Traces contain
business data: use them on development tenants.

Uses the operations command of the Web UI (there is no OData API for it).
```

**Usage:** `cpictl log-level [flags]`

**Flags:**

```
      --artifact-id string           Integration flow ID (deployed)
      --level string                 NONE, INFO, DEBUG or TRACE
      --node-type string             Runtime node type (default "IFLMAP")
      --runtime-location-id string   Runtime location (edge integration cells use their own) (default "cloudintegration")
```

**Examples:**

```
  cpictl log-level --artifact-id OrderIntake --level TRACE
```

## logs

Query message processing logs

```
Query message processing logs (MPL) of the runtime, newest first.

--since/--until take a duration back from now (30m, 2h, 1d) or an RFC 3339
timestamp. --wait polls until at least one matching message exists and all
matching messages reached a final status (COMPLETED, FAILED, ESCALATED,
CANCELLED, DISCARDED, ABANDONED); use it after sending a test message with
--since set to the send time. Exit code 6 if the wait times out.

Statuses: COMPLETED, PROCESSING, RETRY, ESCALATED, FAILED, CANCELLED, DISCARDED, ABANDONED
```

**Usage:** `cpictl logs [flags]`

**Flags:**

```
      --application-message-id string   Application message ID
      --artifact-id string              Integration flow ID
      --correlation-id string           Correlation ID
      --errors                          Include the error text of failed messages
      --header string                   Only messages with this custom header property, name=value (client-side scan; needs --artifact-id or --package-id, and --since)
      --package-id string               Messages of all integration flows of this package (needs --since)
      --since string                    Messages that ended after this time (duration like 1h or RFC 3339)
      --skip int                        Skip the first n messages
      --status strings                  Comma separated statuses, e.g. FAILED,RETRY
      --top int                         Maximum number of messages (max 200) (default 20)
      --until string                    Messages that started before this time (duration like 1h or RFC 3339)
      --wait duration                   Wait up to this long for final messages (e.g. 60s)
```

**Examples:**

```
  cpictl logs --artifact-id OrderIntake --since 1h
  cpictl logs --artifact-id OrderIntake --status FAILED --errors --output json
  cpictl logs --artifact-id OrderIntake --since 2m --wait 60s --errors
  cpictl logs --package-id Orders --since 1h --header OrderId=4711
  cpictl logs get --message-guid AFq478Bblxi4wCjBcDb_G0vAGGZG
  cpictl logs tree --trace-id 0af7651916cd43dd8448eb211c80319c
```

## logs attachment

Download a log attachment (ID from 'logs get')

**Usage:** `cpictl logs attachment [flags]`

**Flags:**

```
      --id string       Attachment ID
      --max-bytes int   Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --out string      Write the content to this file instead of stdout / the JSON result
```

## logs get

Show one message: status, error text, custom headers, attachments

**Usage:** `cpictl logs get [flags]`

**Flags:**

```
      --message-guid string   Message GUID
```

## logs payload

Download a persisted message (message store entry ID from 'logs get')

**Usage:** `cpictl logs payload [flags]`

**Flags:**

```
      --id string       Message store entry ID
      --max-bytes int   Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --out string      Write the content to this file instead of stdout / the JSON result
```

## logs steps

Show the processing steps of a message and the step that failed

**Usage:** `cpictl logs steps [flags]`

**Flags:**

```
      --message-guid string   Message GUID
```

## logs summary

Message volume and failures per flow, per connection between flows and per error fingerprint

```
Summarize the message processing logs of a time window: per flow the count per
status, failures, average and longest duration; per connection between flows the
messages and failures (linked by the logs' predecessor, or for the connections of
the content graph by shared correlation ID); failures grouped by error fingerprint
(the error text without IDs, timestamps and long numbers) with a sample, count,
first and last occurrence. Reads at most --max-messages, newest first.
```

**Usage:** `cpictl logs summary [flags]`

**Flags:**

```
      --artifact strings    Only these flows (IDs or patterns)
      --error-samples int   Error texts read for fingerprints (one request each) (default 50)
      --graph string        Content graph for connections without predecessor links (default: .cpi/graph.json if it exists)
      --max-messages int    Messages read at most, newest first (default 5000)
      --since string        Start of the window (duration like 1h or RFC 3339) (default "1h")
      --until string        End of the window (default: now)
```

**Examples:**

```
  cpictl logs summary --since 1h
  cpictl logs summary --since 24h --artifact 'Orders_*' --graph .cpi/graph.json --output json
```

## logs trace

List the traced steps of a message (flow on log level TRACE)

```
List the steps of a message that was processed with log level TRACE and the
trace IDs of the message at each step. Read one with 'logs trace-message'.
Set the level with 'cpictl log-level --level TRACE' (active for 10 minutes).
```

**Usage:** `cpictl logs trace [flags]`

**Flags:**

```
      --message-guid string    Message GUID
      --model-step-id string   Only this element of the iFlow model
```

## logs trace-message

Show payload, headers and exchange properties of a traced step (ID from 'logs trace')

**Usage:** `cpictl logs trace-message [flags]`

**Flags:**

```
      --id string       Trace ID
      --max-bytes int   Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --out string      Write the content to this file instead of stdout / the JSON result
```

## logs tree

Show the path of a message or trace across flows and its first failure

```
Build the call tree across flows from a trace ID or from any message GUID.

--trace-id (W3C trace ID, e.g. the traceId of 'cpictl send'): messages are found by
ApplicationMessageId = trace ID, otherwise by scanning the scope (--artifact-ids or
--package-id, and --since) for the trace-id custom header.

--message (a message GUID): all messages with its correlation ID. With --key-header
(custom header properties your flows write, e.g. OrderNo), messages of the scope with
the same value join the path, with the rest of their run: the path continues when the
message left the tenant and came back with a new correlation ID. The tenant cannot
filter by custom headers, so the scope is scanned (at most --max-scan messages).

Nodes are linked by span-id / parent-span-id (names configurable), else by the
predecessor message, else (--message only) by start time ("inferred", or "header"
across correlation IDs). The result has the hops between flows and a path key that
is the same for every message taking the same route.
```

**Usage:** `cpictl logs tree [flags]`

**Flags:**

```
      --artifact-ids strings     Scope of the fallback scan
      --key-header strings       With --message: custom header properties that join runs with the same value (scans the scope)
      --max-messages int         With --message: maximum messages of the path (default 200)
      --max-scan int             Maximum messages scanned (default 200)
      --message string           Message GUID: follow its correlation ID (and --key-header) across flows
      --package-id string        Scope of the fallback scan: all flows of this package
      --parent-property string   Custom header property with the parent span ID (default "parent-span-id")
      --since string             Start of the scan window (duration like 1h or RFC 3339)
      --span-property string     Custom header property with the span ID (default "span-id")
      --trace-id string          Trace ID (32 hex characters)
      --trace-property string    Custom header property with the trace ID (default "trace-id")
      --until string             End of the scan window
```

**Examples:**

```
  cpictl logs tree --trace-id 0af7651916cd43dd8448eb211c80319c
  cpictl logs tree --message AGXyz... --key-header OrderNo --package-id Orders --since 2h
```

## matrix

Version matrix: every artifact's version in Git and on each tier, and what is ready to promote

```
Show per artifact the Bundle-Version in the content tree (--dir) and on each tier the
designtime version, draft flag, running version and status, and who changed it last
where the tenant reports it. Tiers are given in promotion order:

  tenant                 the configured tenant
  tenant:<profile>       the tenant of a profile (named after the profile)
  <name>=tenant:<profile>

"behind" lists the tiers whose version is lower than the tier before them (or Git):
the next promotions. One pass per tenant (package lists and the runtime list), no
downloads.
```

**Usage:** `cpictl matrix <tier>... [flags]`

**Flags:**

```
      --artifact strings   Only these artifacts (IDs or patterns)
      --differences        Only artifacts whose versions differ
      --dir string         Content tree with the Git versions (default: packages if it exists)
      --package strings    Only these packages (IDs or patterns)
```

**Examples:**

```
  cpictl matrix tenant:dev tenant:test tenant:prod --dir packages
  cpictl matrix DEV=tenant:dev PROD=tenant:prod --package Orders --differences --output json
```

## mcp

Run the MCP server (stdio) for AI agents

```
Run a Model Context Protocol server on stdin/stdout.

The server uses the same tenant settings as every other command (flags,
CPICTL_* environment variables or cpictl.yaml). stdout carries the
protocol only; logs go to stderr as JSON lines.

Tools: list packages, artifacts, resources and parameters; create packages;
download, upload, validate, guideline check, deploy, undeploy (requires
confirm=true); runtime status and endpoints; send test messages; message logs,
steps, attachments and persisted messages; discovery of existing content;
pd_deploy (dry run unless dry_run=false). See docs/mcp.md.

send_test_message uses the --runtime-* credentials (default: the API
credentials), see 'cpictl send --help'.

Local paths given to tools are resolved against --root and may not leave it.

Limit the tools per server, e.g. for a QA or production tenant:
  --read-only                     no tool that changes the tenant or sends messages
  --tools list_*,get_*            only matching tools
  --disable-tools undeploy,pd_*   everything except these
  --toolset build,test            only the tools of these tasks
  --dynamic-toolsets              start with a few tools; the agent enables toolsets as needed
  --mode discover|operate|develop|full presets (combined with the above, the most
                                  restrictive wins)
Also as CPICTL_MODE, CPICTL_TOOLSET, CPICTL_DYNAMIC_TOOLSETS, CPICTL_READ_ONLY, CPICTL_TOOLS, CPICTL_DISABLE_TOOLS. Disabled tools are not
listed and cannot be called; a pattern that matches no tool is an error.
```

**Usage:** `cpictl mcp [flags]`

**Flags:**

```
      --cache-ttl int                       Seconds list_packages and list_artifacts results are reused (0: no cache); any tool that changes the tenant clears them (default 60)
      --disable-tools strings               Do not offer these tools (names or patterns); wins over --tools
      --dynamic-toolsets                    List only help, doctor, list_toolsets and enable_toolset at the start; the agent enables the toolsets it needs (the client must support tools/list_changed). --toolset then names the toolsets enabled at the start
      --max-checks int                      Default maximum number of deploy/undeploy status checks (default 30)
      --mode string                         Preset: discover (read-only), operate (read tools + set_log_level), develop (all tools, no pd_deploy full_sync), full (all tools, no restrictions)
      --poll-interval int                   Default seconds between deploy/undeploy status checks (default 10)
      --read-only                           Offer only tools that do not change the tenant or trigger processing
      --root string                         Directory that local paths of tool calls are confined to (default ".")
      --runtime-oauth-clientid string       OAuth client ID for runtime endpoints (default: the API credentials)
      --runtime-oauth-clientsecret string   OAuth client secret for runtime endpoints
      --runtime-oauth-host string           OAuth token server host for runtime endpoints (default: --oauth-host)
      --runtime-password string             Password for Basic Auth on runtime endpoints
      --runtime-userid string               User ID for Basic Auth on runtime endpoints
      --tools strings                       Offer only these tools (names or patterns such as list_*)
      --toolset strings                     Offer only the tools of these tasks: build, improve, inspect, monitor, partner-directory, promote, security, test (combined with --tools; the mode still applies)
      --versioning string                   Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

**Examples:**

```
  # Claude Code / any MCP client configuration
  {"mcpServers": {"cpi": {"command": "cpictl", "args": ["mcp", "--root", "/path/to/repo"],
    "env": {"CPICTL_TMN_HOST": "...", "CPICTL_OAUTH_HOST": "...",
            "CPICTL_OAUTH_CLIENTID": "...", "CPICTL_OAUTH_CLIENTSECRET": "..."}}}}
```

## mock-tenant

Run an in-memory SAP CPI tenant for local development and tests (never a real tenant)

```
Serve an in-memory mock of the SAP CPI APIs that cpictl uses: packages,
artifacts (download, upload, deploy, undeploy), parameters, runtime status,
endpoints, message processing logs with custom headers, steps and errors,
credentials, keystore and Partner Directory. Any credentials are accepted
(Basic Auth with CSRF, or OAuth client credentials at /oauth/token).

--seed demo loads a demo landscape for --tier dev|test|prod:
  Orders_In -> (ProcessDirect) Orders_Route -> (JMS) Billing_In -> (ProcessDirect)
  Billing_Post, Partner_Notify (timer, SFTP), and on dev Returns_In; a day of
  messages with the OrderNo custom header and some failures. The tiers differ
  like real ones: dev is ahead (newer Orders_Route, a new flow, Billing_Post in
  draft), prod has a parameter changed on the tenant, a certificate expiring in
  20 days and no Returns_API credential.
--seed empty starts without content.
--seed-dir loads a landscape directory instead (landscape.yaml and content in
the layout cpictl snapshot writes, see docs/mock-tenant.md): its tiers, systems
and traffic; --tier names one of its tiers.

The tenant behaves live: a deploy starts the designtime version and registers
the flow's endpoints, uploads are recorded, and a message sent to an endpoint
(POST /http/orders/in) runs the flows: one message processing log per flow,
ProcessDirect and JMS to the next flows, receivers answered by the landscape's
systems (latency, failures). --live-traffic keeps generating messages.
The /_mock admin API needs "Authorization: Bearer <token>": --admin-token, or a random
token generated at start and printed as CPICTL_MOCK_ADMIN_TOKEN.
State is in memory; a restart resets it.

Plain http is accepted by cpictl for loopback hosts only. To reach the mock
from another container use --tls: a CA and server certificate are generated
for --tls-hosts, the CA is written to --ca-out; point the client at it with
SSL_CERT_FILE.
```

**Usage:** `cpictl mock-tenant [flags]`

**Flags:**

```
      --addr string              Listen address (default "127.0.0.1:8081")
      --admin-token string       Bearer token for the /_mock admin API (default: a random token, printed at start)
      --ca-out string            With --tls: file the generated CA certificate is written to (PEM)
      --live-traffic float       Keep generating the landscape's traffic in real time at this speed (1: as in landscape.yaml, 60: an hour per minute; 0: off)
      --log-retention duration   How long message logs are kept (older ones are dropped; at most 100000 are kept; 0: no age limit) (default 168h0m0s)
      --public-url string        Base URL of the flows' runtime endpoints as clients reach the mock, e.g. https://mock-dev:8443 (default: the listen address)
      --seed string              Content: demo or empty (default "demo")
      --seed-dir string          Landscape directory to load instead of --seed (landscape.yaml + packages/)
      --tier string              Tier of the landscape (demo: dev, test, prod) (default "dev")
      --tls                      Serve HTTPS with a generated certificate (for access from other containers)
      --tls-hosts strings        Host names and IPs of the generated certificate (default [localhost,127.0.0.1])
```

**Examples:**

```
  cpictl mock-tenant --tier dev --addr 127.0.0.1:8081
  cpictl mock-tenant --seed-dir ./landscapes/retail-b --tier prod-eu --addr 127.0.0.1:8084
  cpictl mock-tenant --tier prod --addr 0.0.0.0:8443 --tls --tls-hosts mock-prod,localhost --ca-out /certs/mock-ca.pem
```

## number-ranges

List number ranges

**Usage:** `cpictl number-ranges`

## orchestrator

Update and deploy many packages from a local directory tree

```
Orchestrate the complete deployment lifecycle for SAP CPI artifacts.

This command handles:
  - Updates artifacts in SAP CPI tenant with modified MANIFEST.MF and parameters
  - Deploys artifacts to make them active (in parallel for faster execution)
  - Supports deployment prefixes for multi-environment scenarios
  - Intelligent artifact grouping by type for efficient deployment
  - Filter by specific packages or artifacts
  - Load configs from files, folders, or remote URLs
  - Configure via YAML file for repeatable deployments

Configuration Sources:
  The --deploy-config flag accepts:
  - Single file:      ./001-deploy-config.yml
  - Folder:           ./configs (processes all matching files alphabetically)
  - Remote URL:       https://raw.githubusercontent.com/org/repo/main/config.yml (public, no authentication)

  All flags can also be set in the global config file (--config) under
  the 'orchestrator' key; CLI flags override config file settings.

Operation Modes:
  --update          Update and deploy artifacts (default)
  --update-only     Only update artifacts, don't deploy
  --deploy-only     Only deploy artifacts, don't update

Deployment Strategy:
  1. Update Phase: All packages and artifacts are updated first
  2. Deploy Phase: All artifacts are deployed in parallel
     - Deployments are triggered concurrently per package
     - Status is polled for all deployments simultaneously
     - Configurable parallelism and retry settings

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'orchestrator' section. CLI flags override config file settings.
```

**Usage:** `cpictl orchestrator [flags]`

**Flags:**

```
      --artifact-filter string     Comma-separated artifacts to include: artifact IDs, artifact folders or source IDs (a folder or source ID selects every ID deployed from it) (config: orchestrator.artifactFilter)
      --config-pattern string      File pattern for config files in folders (config: orchestrator.configPattern) (default "*.y*ml")
      --defer-deploy               Do not deploy: add the deployments to the pending file for one 'cpictl deploy --pending' at the end
  -c, --deploy-config string       Path to deployment config file/folder/URL (config: orchestrator.deployConfig)
      --deploy-delay int           Seconds between deployment status checks (config: orchestrator.deployDelaySeconds) (default 10)
      --deploy-only                Only deploy artifacts, don't update
      --deploy-retries int         Deployment status checks per artifact (config: orchestrator.deployRetries) (default 30)
  -p, --deployment-prefix string   Deployment prefix for package/artifact IDs (config: orchestrator.deploymentPrefix)
      --draft-handling string      Artifacts in draft on the tenant (someone edits them in the Web UI): SKIP (not uploaded, not deployed, reported) or ERROR (the run fails) (config: orchestrator.draftHandling) (default "SKIP")
      --fail-on-draft              Same as --draft-handling ERROR
      --keep-temp                  Keep temporary directory after execution (config: orchestrator.keepTemp)
      --merge-configs              Merge multiple configs into single deployment (config: orchestrator.mergeConfigs)
      --package-filter string      Comma-separated list of packages to include (config: orchestrator.packageFilter)
  -d, --packages-dir string        Directory containing packages (config: orchestrator.packagesDir)
      --parallel int               Artifacts uploaded at the same time, across all packages (config: orchestrator.parallel) (default 8)
      --parallel-deployments int   Deployments at the same time per package (config: orchestrator.parallelDeployments) (default 5)
      --pending-file string        Pending deployments file (default: .cpi/pending-deploy.json)
      --plan                       Only show what would be uploaded and deployed, and why; nothing is written to the tenant
      --snapshot-state string      Snapshot state of the target tenant (written by snapshot) used instead of downloading artifacts for the comparison (config: orchestrator.snapshotState; default: .cpi/snapshot-state.json in the current directory or above --packages-dir; "off": always download)
      --update                     Update and deploy artifacts
      --update-only                Only update artifacts, don't deploy
      --verify-download            Download every existing artifact for the comparison, even when the snapshot state covers it (config: orchestrator.verifyDownload)
      --versioning string          Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

**Examples:**

```
  # Update and deploy with config from global cpictl.yaml
  cpictl orchestrator --update

  # Load specific config file
  cpictl orchestrator --config ./my-config.yml --update

  # Override settings via CLI flags
  cpictl orchestrator --config ./my-config.yml \
    --deployment-prefix DEV --parallel-deployments 5
```

## packages

List integration packages

**Usage:** `cpictl packages`

## packages create

Create an integration package if it does not exist

```
Create an integration package. An existing package with the same ID is left
unchanged (action EXISTS). To create or update a package from a JSON file use
'update package'.
```

**Usage:** `cpictl packages create [flags]`

**Flags:**

```
      --description string   Description
      --name string          Display name (default: package ID)
      --package-id string    Package ID (letters, digits, '_' and '.')
      --short-text string    Short description (default: name)
```

**Examples:**

```
  cpictl packages create --package-id SalesOrders --name "Sales Orders"
```

## params

Read or change externalised parameters of an integration flow

## params get

Show the parameters of an integration flow

**Usage:** `cpictl params get [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --version string       Designtime version (default "active")
```

## params set

Set parameters of an integration flow (deploy afterwards to activate)

**Usage:** `cpictl params set [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --dry-run              Show what would change without writing
      --param stringArray    Parameter as key=value (repeatable)
      --version string       Designtime version (default "active")
```

**Examples:**

```
  cpictl params set --artifact-id MyIFlow --param Host=example.com --param Port=443
```

## pd

Inspect Partner Directory parameters (get, diff, deps)

## pd deps

Show which flows read which Partner Directory parameters (local files)

```
Scan local content for Partner Directory references: pd:<PID>:<ID>:<Binary|String>
in .iflw models, dynamic pd:${...} references and getParameter(id, pid, ...) in
Groovy scripts. With --resources-path, PIDs that are referenced but have no local
directory are listed as unknown. Nothing is read from the tenant.
```

**Usage:** `cpictl pd deps [flags]`

**Flags:**

```
      --id string               Only this parameter ID
      --local-dir string        Local content directory (default ".")
      --pid string              Only this partner ID
      --resources-path string   Local Partner Directory tree (to report unknown PIDs)
```

**Examples:**

```
  cpictl pd deps --local-dir ./content --resources-path ./partner-directory --pid ONE_OMS
```

## pd diff

Compare local Partner Directory files with the tenant

```
Compare the local tree (layout of pd-snapshot) with the tenant: per parameter
create, update, unchanged or remote_only (only on the tenant: pd-deploy --full-sync
would delete it). Exit code 7 when a PID could not be read locally.
```

**Usage:** `cpictl pd diff [flags]`

**Flags:**

```
      --pids strings            Only these partner IDs
      --resources-path string   Path to partner directory parameters (default "./partner-directory")
```

**Examples:**

```
  cpictl pd diff --resources-path ./partner-directory --pids ONE_OMS
```

## pd get

Show the Partner Directory parameters of a partner ID on the tenant

**Usage:** `cpictl pd get [flags]`

**Flags:**

```
      --content         Include binary content
      --key strings     Only these parameter IDs (repeatable)
      --max-bytes int   Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --out string      Write the content to this file instead of stdout / the JSON result
      --pid string      Partner ID
```

**Examples:**

```
  cpictl pd get --pid ONE_OMS
  cpictl pd get --pid ONE_OMS --key now_email --content
```

## pd-deploy

Upload Partner Directory parameters from local files

```
Upload all partner directory parameters from local files to SAP CPI.

This command reads partner directory parameters from a local directory structure
and uploads them to the SAP CPI Partner Directory:

  {PID}/
    String.properties    - String parameters as key=value pairs
    Binary/              - Binary parameters as individual files
      {ParamId}.{ext}    - Binary parameter files
      _metadata.json     - Content type metadata

The deploy operation supports several modes:
  - Replace mode (default): Updates existing parameters with local values
  - Add-only mode: Only creates new parameters, skips existing ones
  - Full sync mode: Deletes remote parameters not present locally (local is source of truth)

See docs/partner-directory.md for the file format and full sync safety rules.
```

**Usage:** `cpictl pd-deploy [flags]`

**Flags:**

```
      --dry-run                 Show what would be changed without making changes
      --full-sync               Delete remote parameters not present locally (local is source of truth)
      --keys strings            Deploy only these parameters, PID:ID (create or update, never delete; not with --full-sync)
      --pids strings            Comma separated list of Partner IDs to deploy (e.g., 'PID1,PID2')
      --replace                 Replace existing values (false = add only missing values) (default true)
      --resources-path string   Path to partner directory parameters (default "./partner-directory")
```

**Examples:**

```
  # Deploy in add-only mode (don't update existing parameters)
  cpictl pd-deploy --replace=false

  # Deploy with full sync (delete remote parameters not in local)
  cpictl pd-deploy --full-sync

  # Deploy only specific PIDs
  cpictl pd-deploy --pids "SAP_SYSTEM_001,CUSTOMER_API"

  # Dry run to see what would be changed
  cpictl pd-deploy --dry-run

  # Only one mapping (no other parameter is touched)
  cpictl pd-deploy --keys ONE_OMS:now_email
```

## pd-snapshot

Download Partner Directory parameters into local files

```
Download all partner directory parameters from SAP CPI and save them locally.

This command retrieves both string and binary parameters from the SAP CPI Partner Directory
and organizes them in a local directory structure:

  {PID}/
    String.properties    - String parameters as key=value pairs
    Binary/              - Binary parameters as individual files
      {ParamId}.{ext}    - Binary parameter files
      _metadata.json     - Content type metadata

The snapshot operation supports two modes:
  - Replace mode (default): Overwrites existing local files
  - Add-only mode: Only adds new parameters, preserves existing values

See docs/partner-directory.md for the file format and full sync safety rules.
```

**Usage:** `cpictl pd-snapshot [flags]`

**Flags:**

```
      --pids strings            Comma separated list of Partner IDs to snapshot (e.g., 'PID1,PID2')
      --replace                 Replace existing values (false = add only missing values) (default true)
      --resources-path string   Path to save partner directory parameters (default "./partner-directory")
```

**Examples:**

```
  # Snapshot in add-only mode (don't overwrite existing values)
  cpictl pd-snapshot --replace=false

  # Snapshot only specific PIDs
  cpictl pd-snapshot --pids "SAP_SYSTEM_001,CUSTOMER_API"
```

## profile

Switch between tenants (profiles in $HOME/.cpictl)

```
A profile is a personal config file $HOME/.cpictl/<name>.yaml with the connection
of one tenant (same keys as cpictl.yaml, credentials included; keep it chmod 600).

  cpictl profile use qa          make qa the default for every following command
  cpictl --profile dev deploy …  one command against another tenant
  CPICTL_PROFILE=dev             per shell (overrides 'profile use')
  cpictl mcp --profile dev       one MCP server per tenant

Precedence: --config, --profile, CPICTL_PROFILE, CPICTL_CONFIG, 'profile use',
$HOME/cpictl.yaml. A repository's cpictl.yaml is still overlaid; its hosts must
match the profile's.
```

## profile current

Show the profile that commands use now

**Usage:** `cpictl profile current`

## profile list

List profiles and mark the active one

**Usage:** `cpictl profile list`

## profile use

Make a profile the default (stored in $HOME/.cpictl/current); 'use -' clears it

**Usage:** `cpictl profile use <name>`

## resources

List the resources (scripts, mappings, schemas, ...) of an integration flow

**Usage:** `cpictl resources [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --version string       Designtime version (default "active")
```

**Examples:**

```
  cpictl resources --artifact-id OrderIntake
  cpictl resources get --artifact-id OrderIntake --name script1.groovy --type groovy
```

## resources get

Download one resource of an integration flow

```
Download one resource. In text mode the content is written to stdout (or
--out); with --output json it is part of the result document.
```

**Usage:** `cpictl resources get [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --max-bytes int        Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --name string          Resource name, e.g. script1.groovy
      --out string           Write the content to this file instead of stdout / the JSON result
      --type string          Resource type, e.g. groovy, xslt, mmap, xsd, wsdl, jar
      --version string       Designtime version (default "active")
```

## send

Send a test message to a deployed integration flow

```
Send a test message to an endpoint of a deployed integration flow and print
the HTTP status, the response and the message GUID. Only endpoint URLs that the
tenant lists for the flow (see 'endpoints') are used.

With --wait the command also waits for the message processing log and exits
with code 5 when the message did not complete. The message is processed like any
other, including calls to receivers: use test data on a development tenant.

Runtime endpoints usually need other credentials than the API: set
--runtime-oauth-clientid/--runtime-oauth-clientsecret (CPICTL_RUNTIME_OAUTH_*)
from a service key of plan integration-flow with role ESBMessaging.send.
Without them the API credentials are used.

Flows started by ProcessDirect have no endpoint of their own: --process-direct
sends the message to the test harness flow (--harness), which forwards it to the
given address; --artifact-id is then the flow behind the address and --wait
reports that flow's message (found by correlation ID). See docs/testing.md.
```

**Usage:** `cpictl send [flags]`

**Flags:**

```
      --artifact-id string                  Integration flow ID (deployed)
      --body string                         Message body
      --body-file string                    Read the message body from this file ('-' for stdin)
      --content-type string                 Content-Type of the body
      --harness string                      Test harness flow ID (with --process-direct) (default "CPICTL_Test_Harness")
      --header strings                      Additional header name=value (repeatable)
      --method string                       HTTP method (default "POST")
      --no-trace                            Do not send a W3C traceparent header (by default one is generated unless --header traceparent=... is given; its traceId is in the result)
      --process-direct string               Send through the test harness flow to this ProcessDirect address (flows without an HTTP sender)
      --runtime-oauth-clientid string       OAuth client ID for runtime endpoints (default: the API credentials)
      --runtime-oauth-clientsecret string   OAuth client secret for runtime endpoints
      --runtime-oauth-host string           OAuth token server host for runtime endpoints (default: --oauth-host)
      --runtime-password string             Password for Basic Auth on runtime endpoints
      --runtime-userid string               User ID for Basic Auth on runtime endpoints
      --url string                          Endpoint URL, only needed when the flow has several endpoints
      --wait duration                       Wait up to this long for the message processing log (e.g. 60s)
```

**Examples:**

```
  # Send a file and wait for the outcome
  cpictl send --artifact-id OrderIntake --body-file order.xml --content-type application/xml --wait 60s

  # Body from stdin, extra header
  echo '{"id":1}' | cpictl send --artifact-id OrderIntake --body-file - --header X-Test=1

  # A flow with a ProcessDirect sender, through the test harness
  cpictl send --artifact-id Billing --process-direct /billing/in --body-file invoice.xml --wait 60s
```

## skills

List, read and install the cpi skills built into cpictl

```
The skills of the Claude Code plugin (cpi-discover, cpi-plan, cpi-build, cpi-test,
cpi-review) are built into cpictl, in the version of this binary. Use them in
agents other than Claude Code (Codex, Cursor, Gemini CLI, ...), or read them.
Claude Code users install the plugin instead (docs/plugin.md); over MCP, the
help tool shows the same skills.
```

## skills install

Copy the skills into the skill folder of an agent

```
Copy the skills into a content repository (default: the current directory) or,
with --user, into your home directory, in the folder the agent reads:

  agents, codex   .agents/skills   (Codex; Cursor and others read it too)
  cursor          .cursor/skills
  gemini          .gemini/skills
  opencode        .opencode/skills (--user: ~/.config/opencode/skills; OpenCode
                  also reads .agents/skills and .claude/skills)
  claude          .claude/skills   (Claude Code without the plugin)

Existing cpi-* skills there are replaced; other skills are kept. The
allowed-tools line is removed, it names tools as the Claude Code plugin sees
them. Run it again after updating cpictl.
```

**Usage:** `cpictl skills install [REPO] [flags]`

**Flags:**

```
      --agent string   Agent whose skill folder to use: agents, claude, codex, cursor, gemini, opencode (default "agents")
      --user           Install into your home directory instead of a repository
```

**Examples:**

```
  cpictl skills install --agent codex
  cpictl skills install --agent cursor ../content-repo
  cpictl skills install --agent gemini --user
  cpictl skills install --agent opencode
```

## skills list

List the skills and what they are for

**Usage:** `cpictl skills list`

## skills show

Print a skill's SKILL.md or one of its reference files

**Usage:** `cpictl skills show SKILL [FILE]`

**Examples:**

```
  cpictl skills show cpi-build
  cpictl skills show cpi-plan brief-template.md
```

## snapshot

Save all integration packages of the tenant to a Git repository

```
Snapshot all editable integration packages from SAP Integration Suite
tenant to a Git repository.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'snapshot' section. CLI flags override config file settings.
```

**Usage:** `cpictl snapshot [flags]`

**Flags:**

```
      --deploy-config string        Deploy config file or folder of the orchestrator: its deployment copies are not written (config: snapshot.deployConfig, else orchestrator.deployConfig)
      --deployment-prefix strings   Additional deployment prefixes whose copies are not written (the deploy config's deploymentPrefix always counts)
      --draft-handling string       Handling when artifact is in draft version. Allowed values: SKIP, ADD, ERROR (config: snapshot.draftHandling) (default "SKIP")
      --dry-run                     Only report per artifact what the snapshot would do (new, changed, deleted, unchanged, local-modified, derived); writes no files and no state
      --fail-on-local-modified      Exit with code 5 when an artifact is local-modified (for CI)
      --git-commit-email string     Email used in commit (config: snapshot.gitCommitEmail) (default "41898282+github-actions[bot]@users.noreply.github.com")
      --git-commit-msg string       Message used in commit (config: snapshot.gitCommitMsg) (default "Tenant snapshot of <current time>")
      --git-commit-user string      User used in commit (config: snapshot.gitCommitUser) (default "github-actions[bot]")
      --git-skip-commit             Skip committing changes to Git repository (config: snapshot.gitSkipCommit)
      --include-derived             Write deployment copies like any other artifact (ignore the deploy config)
      --incremental                 Skip the download of artifacts whose version, ModifiedAt, configured parameters and local copy did not change since the last snapshot (config: snapshot.incremental)
      --keep-orphan-parameters      Write parameters.prop keys that parameters.propdef no longer declares (values the tenant keeps after a parameter was renamed or removed); they are reported either way (config: snapshot.keepOrphanParameters)
      --overwrite-local             Overwrite artifacts with local edits since the last snapshot (default: skip them as local-modified)
      --parallel int                Artifacts downloaded at the same time, across all packages (config: snapshot.parallel) (default 8)
      --prune                       Remove local artifact folders of artifacts and packages deleted on the tenant, and local folders of derived copies (never when edited locally)
      --state-file string           State of the last snapshot (default: <dir-git-repo>/.cpi/snapshot-state.json, committed with the snapshot) (config: snapshot.stateFile)
      --sync-package-details        Sync details of Integration Packages (config: snapshot.syncPackageDetails) (default true)
```

## snapshot restore

Create or update integration packages on the tenant from a Git repository

```
Restore all editable integration packages from a Git repository to SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'restore' section. CLI flags override config file settings.
```

**Usage:** `cpictl snapshot restore [flags]`

**Flags:**

```
      --versioning string   Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

## stats

Show local usage statistics (which commands and MCP tools run, how often, how long)

```
Show local usage statistics: runs, failures and durations per command and MCP tool.

Every command and MCP tool call appends one line (name, source cli/mcp, exit code,
duration, time) to $HOME/.cpictl/stats.jsonl. No arguments, hosts or artifact names
are recorded, and nothing is sent anywhere. The file is compacted to its newest half
when it reaches 1 MiB. CPICTL_STATS=off turns recording off; --reset deletes the file.
```

**Usage:** `cpictl stats [flags]`

**Flags:**

```
      --reset           Delete the statistics file
      --since string    Only runs in this period, e.g. 24h or 7d
      --source string   Only cli or mcp
```

**Examples:**

```
  cpictl stats
  cpictl stats --since 7d --source mcp
  cpictl stats --reset
```

## status

Show runtime status, version and errors of artifacts

```
Show the runtime status of artifacts. With --artifact-ids only those
artifacts are shown (including NOT_DEPLOYED ones); without, all deployed
artifacts, optionally filtered with --runtime-status (e.g. ERROR).
```

**Usage:** `cpictl status [flags]`

**Flags:**

```
      --artifact-ids strings     Comma separated list of artifact IDs (default: all deployed artifacts)
      --runtime-status strings   Without --artifact-ids: only artifacts in these statuses (STARTED, STARTING, ERROR, STOPPING)
```

**Examples:**

```
  cpictl status --artifact-ids MyIFlow,MyMapping --output json
  cpictl status --runtime-status ERROR
```

## sync

Synchronise the artifacts of a package between tenant and Git

```
Synchronise designtime artifacts between SAP Integration Suite
tenant and a Git repository.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'sync' section. CLI flags override config file settings.
```

**Usage:** `cpictl sync [flags]`

**Flags:**

```
      --dir-naming-type string          Name artifact directory by ID or Name. Allowed values: ID, NAME (config: sync.dirNamingType) (default "ID")
      --draft-handling string           Handling when artifact is in draft version. Allowed values: SKIP, ADD, ERROR (config: sync.draftHandling) (default "SKIP")
      --package-id string               ID of Integration Package (config: sync.packageId)
      --script-collection-map strings   Comma-separated source-target ID pairs for converting script collection references during sync (config: sync.scriptCollectionMap)
      --sync-package-details            Sync details of Integration Package (config: sync.syncPackageDetails)
      --versioning string               Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

## sync apiproduct

Synchronise API Management products between tenant and Git

```
Synchronise API Management products between SAP Integration Suite
tenant and a Git repository.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'sync.apiproduct' section. CLI flags override config file settings.
```

**Usage:** `cpictl sync apiproduct`

## sync apiproxy

Synchronise API Management proxies (with dependent artifacts) between tenant and Git

```
Synchronise API Management proxies (with dependent artifacts) between SAP Integration Suite
tenant and a Git repository.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'sync.apiproxy' section. CLI flags override config file settings.
```

**Usage:** `cpictl sync apiproxy`

## transport

Prepare a transport between tiers: dependencies, pre-checks against the target, copy artifact folders

```
Helpers to move artifacts from one tier to the next. The deployment itself is the
orchestrator (or configure and deploy --pending) run against the target tier.

  deps   what the selected artifacts need: script collections and mappings they
         reference, flows they call (ProcessDirect / JMS), credentials and key
         aliases, Partner Directory parameters
  check  is the target ready? Read only: drafts, drift against the target's Git
         content, dependencies and called flows present, credentials and key
         aliases (also when a parameter names them), expiring certificates,
         Partner Directory parameters, and the flows' configuration compared
         with the source tier (missing values, values that travel, the same
         environment-specific value on both); exit code 5 when a check fails.
         --allow-missing turns failures for material someone else provides
         into warnings
  copy   copy the artifact folders into another content tree (a branch or
         repository per tier), replacing them exactly

Artifacts are given by ID or folder name; --with-deps adds the script
collections and mappings they reference.
```

## transport check

Check a transport against the target tenant (read only)

**Usage:** `cpictl transport check <artifact>... [flags]`

**Flags:**

```
      --allow-missing strings   Report these failures as warnings, for material someone else provides on the target: credential, keystore, pd, parameters, dependency
      --configure string        The target tier's configure file or folder: reports parameters without a value there
      --dir string              Source content tree (default: packages if it exists, else the current directory)
      --expiry-days int         Warn about certificates and keys used by the artifacts that expire within this many days on the target (default 30)
      --repo string             Git repository for git: arguments (default ".")
      --source string           Source tenant (tenant:<profile>): its configured parameter values are compared with the target's (default: parameters.prop of --dir)
      --target string           Target tenant: tenant (the configured one) or tenant:<profile> (default "tenant")
      --target-dir string       The target tier's content in Git (a directory or git:<ref>[:<path>]): reports changes made on the target outside the pipeline
      --with-deps               Add the script collections and mappings the artifacts reference
```

**Examples:**

```
  cpictl transport check Orders_In --target tenant:prod --target-dir git:prod:packages \
    --configure config/prod.yaml --with-deps
```

## transport copy

Copy artifact folders into another content tree (local files)

**Usage:** `cpictl transport copy <artifact>... [flags]`

**Flags:**

```
      --dry-run       Only list what would be copied
      --from string   Source content tree: a directory or git:<ref>[:<path>]
      --repo string   Git repository for git: arguments (default ".")
      --to string     Target content tree (directory)
      --with-deps     Also copy the script collections and mappings the artifacts reference
```

**Examples:**

```
  cpictl transport copy Orders_In --from git:dev:packages --to packages --with-deps
  cpictl transport copy Orders_In --from ../repo-dev/packages --to packages --dry-run
```

## transport deps

List what the selected artifacts depend on (local files)

**Usage:** `cpictl transport deps <artifact>... [flags]`

**Flags:**

```
      --dir string   Content tree (default: packages if it exists, else the current directory)
      --with-deps    Add the script collections and mappings the artifacts reference
```

**Examples:**

```
  cpictl transport deps Orders_In Billing --dir packages --with-deps
```

## undeploy

Remove artifacts from runtime and wait until they are gone

```
Undeploy artifacts from the runtime of an SAP Integration Suite tenant.

For each artifact the runtime artifact is deleted and its status is polled
until the tenant no longer knows it (HTTP 404). Artifacts that are not
deployed are reported as NOT_DEPLOYED and are not treated as failures.
Designtime artifacts are not touched.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'undeploy' section. CLI flags override config file settings.
```

**Usage:** `cpictl undeploy [flags]`

**Flags:**

```
      --artifact-ids strings   Comma separated list of artifact IDs (config: undeploy.artifactIds)
      --delay-length int       Delay (in seconds) between each check of artifact undeployment status (config: undeploy.delayLength) (default 10)
      --max-check-limit int    Max number of times to check for artifact undeployment status (config: undeploy.maxCheckLimit) (default 30)
```

**Examples:**

```
  cpictl undeploy --artifact-ids MyIFlow,MyOtherIFlow
```

## update

Create or update designtime artifacts and packages

```
Create or update artifacts and/or integration package on the
SAP Integration Suite tenant.
```

## update artifact

Create or update a designtime artifact from a local directory

```
Create or update artifacts on the
SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'update.artifact' section. CLI flags override config file settings.
```

**Usage:** `cpictl update artifact [flags]`

**Flags:**

```
      --artifact-id string              ID of artifact (config: update.artifact.artifactId)
      --artifact-name string            Name of artifact. Defaults to artifact-id value when not provided (config: update.artifact.artifactName)
      --artifact-type string            Artifact type. Allowed values: Integration, MessageMapping, ScriptCollection, ValueMapping (config: update.artifact.artifactType) (default "Integration")
      --dir-artifact string             Directory containing contents of designtime artifact (config: update.artifact.dirArtifact)
      --dir-work string                 Working directory for in-transit files (config: update.artifact.dirWork) (default "/tmp")
      --file-manifest string            Use a different MANIFEST.MF file instead of the default in META-INF/ (config: update.artifact.fileManifest)
      --file-param string               Use a different parameters.prop file instead of the default in src/main/resources/ (config: update.artifact.fileParam)
      --package-id string               ID of Integration Package (config: update.artifact.packageId)
      --package-name string             Name of Integration Package. Defaults to package-id value when not provided (config: update.artifact.packageName)
      --script-collection-map strings   Comma-separated source-target ID pairs for converting script collection references during create/update (config: update.artifact.scriptCollectionMap)
      --versioning string               Versions: manifest (Bundle-Version of the repository, downgrade guard on), keep (the tenant's versions, guard off) or tenant-bump (max(designtime, runtime)+1); env CPICTL_VERSIONING, set it per pipeline/branch (docs/versioning.md)
```

## update package

Create or update an integration package from a JSON file

```
Create or update integration package on the
SAP Integration Suite tenant.

Configuration:
  Settings can be loaded from the global config file (--config) under the
  'update.package' section. CLI flags override config file settings.
```

**Usage:** `cpictl update package [flags]`

**Flags:**

```
      --package-file string   Path to location of package file (config: update.package.packageFile)
```

## validate

Validate an integration flow on the tenant (like Check in the Web UI)

**Usage:** `cpictl validate [flags]`

**Flags:**

```
      --artifact-id string   Integration flow ID
      --version string       Designtime version (default "active")
```

**Examples:**

```
  cpictl validate --artifact-id OrderIntake
```

## variables

List global and integration flow variables

**Usage:** `cpictl variables [flags]`

**Flags:**

```
      --artifact-id string   Only this flow's variables (plus global ones)
```

**Examples:**

```
  cpictl variables --artifact-id OrderIntake
  cpictl variables get --name lastRun --artifact-id OrderIntake
```

## variables get

Read the value of a variable

**Usage:** `cpictl variables get [flags]`

**Flags:**

```
      --artifact-id string   Integration flow (empty: global variable)
      --max-bytes int        Maximum bytes returned in the JSON result (default 65536; 0 with --out: unlimited)
      --name string          Variable name
      --out string           Write the content to this file instead of stdout / the JSON result
```

## version

Artifact versions in the repository (Bundle-Version)

```
Versions of artifacts live in the repository: Bundle-Version in each artifact's
META-INF/MANIFEST.MF. With --versioning manifest, upload and deploy use exactly
that version on every tenant (docs/versioning.md).
```

## version bump

Raise Bundle-Version of (changed) artifacts

```
Raise Bundle-Version in META-INF/MANIFEST.MF of the artifacts below --dir
(layout <package>/<artifact>). With --changed only artifacts whose directory
changed since the commit that last set their version: committed, staged,
unstaged and untracked changes count. Artifacts never committed keep their
version (new), artifacts whose version was raised since that commit are left
alone (already_bumped), so running it twice does not bump twice.

Run it before the pull request to the development branch and commit the
manifests with the change. No tenant access.
```

**Usage:** `cpictl version bump [flags]`

**Flags:**

```
      --artifact strings   Only these artifact IDs (names or patterns)
      --changed            Only artifacts changed since their version was last set (Git)
      --dir string         Content tree (<package>/<artifact>) (default ".")
      --dry-run            Show the new versions without writing
      --level string       patch, minor or major (default "patch")
      --package strings    Only these package folders (names or patterns)
```

**Examples:**

```
  cpictl version bump --changed --dir packages
  cpictl version bump --changed --level minor --package UtilitiesBaseEDM
  cpictl version bump --artifact UtilitiesBase_MDX_to_EDM_Outbound --dry-run
```
