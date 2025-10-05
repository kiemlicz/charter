# Helm Chart generator

Automatically create and update Helm Charts based on plain remote manifests.

Project originated from the lack of KubeVirt Helm Charts, which makes the process of its upgrade more tedious.    
Remote releases are periodically checked and if new release is available then the new Chart is automatically generated through new Pull Request.  
Once approved, new Chart version is published to `oci://ghcr.io/kiemlicz/charter/`.

# Charts

[Generated Charts catalog](charts/)

## KubeVirt

**Separate [`kubevirt-crds`](charts/kubevirt-crds) must be installed first** 

Chart's `AppVersion` matches [released manifests version](https://storage.googleapis.com/kubevirt-prow/release/kubevirt/kubevirt/stable.txt)  
Chart's `Version` usually matches the `AppVersion`, unless some templating was added and new version has not been released yet. Then the `-beta.N` version is used.

1. Create namespace for KubeVirt, with proper labels. To get right labels, consult
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

2. Inspect the [values](charts/kubevirt/values.yaml), override according to your use case

```bash
helm upgrade kubevirt oci://ghcr.io/kiemlicz/charter/kubevirt --version 1.6.2  
```

## CDI

**Separate [`cdi-crds`](charts/cdi-crds) must be installed first**

Chart's `AppVersion` matches [released manifests version](https://github.com/kubevirt/containerized-data-importer/releases/latest)  
Chart's `Version` usually matches the `AppVersion`, unless some templating was added and new version has not been released yet. Then the `-beta.N` version is used.

1. Create namespace for CDI (or use existing one, like `kubevirt`)

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

2. Inspect the [values](charts/cdi/values.yaml), override according to your use case

```bash
helm upgrade --install cdi oci://ghcr.io/kiemlicz/charter/cdi --version 1.63.1 
```

# Development notes

To add new Chart mind

1. [Link action with package](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility#ensuring-workflow-access-to-your-package).
   Without this the workflow will receive 403

2. Link repo source with package
