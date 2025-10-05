# Helm Chart generator

This project automates the creation and maintenance of Helm Charts from remote Kubernetes manifests.  
It is designed to simplify the upgrade process for projects such as KubeVirt, which lack official Helm Charts.

Remote releases are periodically checked. 
When a new release is detected, a new Chart is automatically generated and a Pull Request is opened. 
Upon approval, the updated Chart version is published to `oci://ghcr.io/kiemlicz/charter/`.

## Chart generation

Chart generation logic is fully customizable via configuration files that use familiar `yq` syntax, 
allowing flexible transformation and templating of upstream manifests.

# Charts

[Browse the generated Charts catalog](charts/)

## KubeVirt

**Note:** Install [`kubevirt-crds`](charts/kubevirt-crds) before deploying the main KubeVirt Chart

- Chart's `AppVersion` matches [released manifests version](https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)  
- Chart's `Version` usually matches the `AppVersion`, unless some templating was added and new version has not been released yet. Then the `-beta.N` version is used.

### Installation steps

1. `helm upgrade --install kubevirt-crds oci://ghcr.io/kiemlicz/charter/kubevirt-crds --version 1.6.2`

2. Create namespace for KubeVirt, with proper labels. To get right labels, consult
   the [KubeVirt release manifests](https://github.com/kubevirt/kubevirt/releases)

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  labels:
    kubevirt.io: ""
    pod-security.kubernetes.io/enforce: privileged
  name: kubevirt
EOF
```

3. Inspect the [values](charts/kubevirt/values.yaml), override according to your use case

```bash
helm upgrade kubevirt oci://ghcr.io/kiemlicz/charter/kubevirt --version 1.6.2  
```

## CDI

**Note:** Install [`cdi-crds`](charts/cdi-crds) before deploying the main CDI Chart

- Chart's `AppVersion` matches [released manifests version](https://github.com/kubevirt/containerized-data-importer/releases/latest)  
- Chart's `Version` usually matches the `AppVersion`, unless some templating was added and new version has not been released yet. Then the `-beta.N` version is used.

### Installation steps

1. `helm upgrade --install cdi-crds oci://ghcr.io/kiemlicz/charter/cdi-crds --version 1.63.1`

2. Create namespace for CDI (or use existing one, like `kubevirt`)

```bash
kubectl apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  labels:
    cdi.kubevirt.io: ""
  name: xcdi
EOF
```

3. Inspect the [values](charts/cdi/values.yaml), override according to your use case

```bash
helm upgrade --install cdi oci://ghcr.io/kiemlicz/charter/cdi --version 1.63.1 
```

# Development notes

To add new Chart mind

1. [Link action with package](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#ensuring-workflow-access-to-your-package).
   Without this the workflow will receive 403

2. Link repo source with package
