# charter — LLM Context

## Project purpose

**charter** is a Go tool that automatically generates and maintains Helm Charts from upstream Kubernetes manifests (e.g. KubeVirt, CDI).

Workflow:
1. **`update` mode** – fetches the latest GitHub release assets, applies `yq`-based transformations declared in `config.yaml`, writes the resulting Helm chart source to `charts/`, commits, pushes a branch, and opens a GitHub PR.
2. **`publish` mode** – packages every chart under `charts/` with `helm package` and pushes to the OCI registry `oci://ghcr.io/kiemlicz/charter/`.

## Repository layout

```
cmd/updater/main.go          – binary entry point; dispatches update vs publish mode
internal/common/api.go       – shared types (Config, GithubRelease, Modification, …)
internal/common/util.go      – helpers (YAML extraction, deep-merge, regex match)
internal/packager/helm.go    – core chart logic: FetchAndUpdate, Prepare, Package, Push, Lint
internal/packager/modifier.go– yq-powered manifest transformer (FilterManifests, ParametrizeManifests)
internal/updater/git/        – go-git wrapper (branch, commit, push)
internal/updater/github/     – GitHub API client (fetch release assets, create PR)
charts/                      – generated Helm chart sources (kubevirt, kubevirt-crds, cdi, cdi-crds)
target/                      – packaged .tgz artefacts (git-ignored in practice)
config.yaml                  – primary runtime config (releases, modifications, helm settings, PR settings)
```

## Key types

| Type | File | Purpose |
|---|---|---|
| `Config` | `internal/common/api.go` | Top-level config loaded via koanf |
| `GithubRelease` | `internal/common/api.go` | One upstream release to track |
| `HelmOps` | `internal/common/api.go` | Per-release Helm generation settings |
| `Modification` | `internal/common/api.go` | Single yq/regex transform rule |
| `Manifests` | `internal/common/api.go` | Raw + CRD manifests with extracted values |
| `HelmizedManifests` | `internal/packager/helm.go` | Result of chart creation (main + CRD chart) |
| `modifier` | `internal/packager/modifier.go` | Stateful yq evaluator; singleton `ChartModifier` |

## Modification system

Each entry under `githubReleases[*].helm.modifications` in `config.yaml` is a `Modification`:

- **`expression`** – a `yq` expression applied to each manifest (or a regex-replacement string when `textRegex` is set).
- **`kind`** – regex; if set, only resources whose `kind` matches are processed.
- **`reject`** – regex; matching resources are skipped.
- **`valuesSelector`** – list of `yq` paths; the current value at each path is extracted and stored in `values.yaml` under the dotted key derived from the matching `{{ .Values.* }}` reference in `expression`.
- **`textRegex`** – applied as a Go `regexp` against the final YAML text; `expression` is the replacement string. Used for Helm helper injection that cannot be expressed as valid YAML during yq evaluation.

Two-pass pipeline per manifest:
1. `ParametrizeManifests` – yq expressions (non-`textRegex` mods).
2. `createTemplates` / `insertHelpers` – text-regex replacements on the serialised YAML.

CRDs are always split into a separate `*-crds` chart.

## Config loading

Uses **koanf** with:
- `config.yaml` file provider
- `--mode` / `--offline` / `--config` CLI flags via pflag
- `GH_TOKEN` env var accepted as `pr.authToken`

## Dependencies (direct)

| Library | Purpose |
|---|---|
| `helm.sh/helm/v3` | Chart creation, packaging, linting, OCI push |
| `github.com/mikefarah/yq/v4` | Manifest transformation |
| `github.com/go-git/go-git/v5` | Git branch / commit / push |
| `github.com/google/go-github/v74` | GitHub API (release assets, PR creation) |
| `github.com/Masterminds/semver/v3` | Semver parsing and comparison |
| `github.com/knadh/koanf/v2` | Configuration loading |
| `github.com/sirupsen/logrus` | Logging |

## Build & run

```bash
# Build
go build ./cmd/updater/

# Run update mode (dry-run, no git ops)
./updater --mode update --offline

# Run update mode (full, requires GH_TOKEN)
GH_TOKEN=<token> ./updater --mode update

# Run publish mode (requires registry login: helm registry login ghcr.io)
./updater --mode publish

# Tests
go test ./...
```

## Conventions

- All log calls go through `common.Log` (logrus logger, set up in `common.Setup`).
- Chart source lives in `charts/` (`helm.srcDir`); packaged `.tgz` artifacts go to `target/` (`helm.targetDir`).
- CRD charts are always named `<chartName>-crds` and must be installed before the main chart.
- `valuesSelector` paths use `yq` dot notation; leading dots are stripped when building the nested `Values` key.
- `textRegex` modifications must come **after** yq modifications in the list (they operate on the serialised YAML text, not on the parsed node).
- Helm templates are stored one file per `kind` (lowercased), e.g. `templates/deployment.yaml`.
- Surrounding quotes around `{{ … }}` in marshalled YAML are stripped by a post-process regex in `materializeManifests`.
- Branch names follow the pattern `update/<chartName>-<appVersion>`.

## Adding a new chart

1. Add a new entry under `githubReleases` in `config.yaml` with `owner`, `repo`, `assets`, and `helm` settings.
2. Provide at least the minimal set of `modifications` to namespace-scope resources and parametrise the CR spec.
3. If the upstream release includes CRDs they will automatically be split into a separate `*-crds` chart.
4. [Link the GitHub Actions workflow to the GHCR package](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#ensuring-workflow-access-to-your-package) and link the repo source to the package.

