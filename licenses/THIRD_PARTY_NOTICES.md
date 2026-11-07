# Third-party software in cpictl

The cpictl binary links the following modules. Each module's license text, and its NOTICE file where it has one (Apache-2.0), is in [third-party/](third-party/).

| Module | Version | License |
|---|---|---|
| `dario.cat/mergo` | v1.0.2 | BSD |
| `github.com/ProtonMail/go-crypto` | v1.3.0 | BSD |
| `github.com/beevik/etree` | v1.8.1 | BSD |
| `github.com/cloudflare/circl` | v1.6.3 | BSD |
| `github.com/cyphar/filepath-securejoin` | v0.6.1 | MPL-2.0 + BSD + MPL-2.0 |
| `github.com/emirpasic/gods` | v1.18.1 | BSD |
| `github.com/fsnotify/fsnotify` | v1.9.0 | BSD |
| `github.com/go-errors/errors` | v1.5.1 | MIT |
| `github.com/go-git/gcfg` | v1.5.1-0.20230307220236-3a3c6141e376 | BSD |
| `github.com/go-git/go-billy/v5` | v5.9.2 | Apache-2.0 |
| `github.com/go-git/go-git/v5` | v5.19.3 | Apache-2.0 |
| `github.com/go-viper/mapstructure/v2` | v2.4.0 | MIT |
| `github.com/godbus/dbus/v5` | v5.2.2 | BSD |
| `github.com/golang/groupcache` | v0.0.0-20241129210726-2c02b8208cf8 | Apache-2.0 |
| `github.com/jbenet/go-context` | v0.0.0-20150711004518-d14ea06fba99 | MIT |
| `github.com/kevinburke/ssh_config` | v1.2.0 | MIT |
| `github.com/magiconair/properties` | v1.18.12 | BSD |
| `github.com/mattn/go-colorable` | v0.1.14 | MIT |
| `github.com/mattn/go-isatty` | v0.0.20 | MIT |
| `github.com/pelletier/go-toml/v2` | v2.2.4 | MIT |
| `github.com/pjbgf/sha1cd` | v0.7.0 | Apache-2.0 |
| `github.com/rs/zerolog` | v1.35.1 | MIT |
| `github.com/sagikazarmark/locafero` | v0.11.0 | MIT |
| `github.com/sergi/go-diff` | v1.4.0 | MIT |
| `github.com/skeema/knownhosts` | v1.3.1 | Apache-2.0 |
| `github.com/sourcegraph/conc` | v0.3.1-0.20240121214520-5f936abd7ae8 | MIT |
| `github.com/spf13/afero` | v1.15.0 | Apache-2.0 |
| `github.com/spf13/cast` | v1.10.0 | MIT |
| `github.com/spf13/cobra` | v1.10.2 | Apache-2.0 |
| `github.com/spf13/pflag` | v1.0.10 | BSD |
| `github.com/spf13/viper` | v1.21.0 | MIT |
| `github.com/subosito/gotenv` | v1.6.0 | MIT |
| `github.com/xanzy/ssh-agent` | v0.3.3 | Apache-2.0 |
| `github.com/zalando/go-keyring` | v0.2.8 | MIT |
| `go.yaml.in/yaml/v3` | v3.0.5 | Apache-2.0 |
| `golang.org/x/crypto` | v0.56.0 | BSD |
| `golang.org/x/net` | v0.57.0 | BSD |
| `golang.org/x/oauth2` | v0.37.0 | BSD |
| `golang.org/x/sys` | v0.47.0 | BSD |
| `golang.org/x/term` | v0.45.0 | BSD |
| `golang.org/x/text` | v0.41.0 | BSD |
| `gopkg.in/warnings.v0` | v0.1.2 | BSD |
| `gopkg.in/yaml.v3` | v3.0.1 | Apache-2.0 |

## Notes

- **FlashPipe** (https://github.com/engswee/flashpipe), Copyright 2021-2025 Eng Swee Yeoh, Apache License 2.0:
  cpictl contains code originally developed as FlashPipe, modified. See [LICENSE](../LICENSE) and
  [NOTICE](../NOTICE).
- **github.com/cyphar/filepath-securejoin** is dual-licensed (BSD-3-Clause and, for some files, MPL-2.0). It is
  used unmodified; its source is available at https://github.com/cyphar/filepath-securejoin.

Regenerate this list after dependency changes: `scripts/third-party-notices.sh`.
