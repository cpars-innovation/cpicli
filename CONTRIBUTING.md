# Contributing

cpictl is developed by cpars innovation. The repository is private; contributions come from the cpictl team and
from people with access granted by cpars innovation.

- Work on a branch and open a pull request against `main`.
- `go test ./...` must pass offline, with no tenant environment variables, in under 60 seconds; CI also runs the
  race detector and fuzzes endpoint routing.
- Changes to security code (see [SECURITY.md](SECURITY.md)) need an independent verification before a release.
- Never use a real tenant or real credentials in tests: use the mock tenant (`internal/cpitest`,
  `cpictl mock-tenant`).
- After a dependency change, regenerate the third-party notices: `scripts/third-party-notices.sh`.
