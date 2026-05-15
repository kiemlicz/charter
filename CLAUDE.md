# charter — LLM Context

## Project purpose

**charter** is a Go tool that automatically generates and maintains Helm Charts from upstream Kubernetes manifests (e.g. KubeVirt, CDI).

Workflow:
1. **`update` mode** – fetches the latest GitHub release assets, applies `yq`-based transformations declared in `config.yaml`, writes the resulting Helm chart source to `charts/`, commits, pushes a branch, and opens a GitHub PR.
2. **`publish` mode** – packages every chart under `charts/` with `helm package` and pushes to the OCI registry `oci://ghcr.io/kiemlicz/charter/`.

## Build & run

```bash
# Build
go build ./cmd/updater/

# Run update mode (dry-run, no git ops)
# modify charts/**/Chart.yaml appVersion to previous version prior to running this, otherwise logic might stop the update as the version is the same as remote 
./updater --mode update --offline

# Run update mode (full, requires GH_TOKEN)
GH_TOKEN=<token> ./updater --mode update

# Run publish mode (requires registry login: helm registry login ghcr.io)
./updater --mode publish

# Tests
go test ./...
```
