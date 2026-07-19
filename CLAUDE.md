# Project purpose

**charter** is a Go tool that automatically generates and maintains Helm Charts from upstream Kubernetes manifests (e.g. KubeVirt, CDI).

Workflow:
1. **`update` mode** – fetches the latest GitHub release assets, applies `yq`-based transformations declared in `config.yaml`, writes the resulting Helm chart source to `charts/`, commits, pushes a branch, and opens a GitHub PR.
2. **`publish` mode** – packages every chart under `charts/` with `helm package` and pushes to the OCI registry `oci://ghcr.io/kiemlicz/charter/`.

# Critical Rules

- Verify before done: never mark a task complete without proving it works (run tests, demonstrate correctness)
