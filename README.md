# KubeVirt Charts

**WIP**  
**Charts not yet published until automated publisher is ready**

While waiting for [official ones](https://github.com/kubevirt/kubevirt/issues/8347) I've created simple app that auto creates the Charts on each
KubeVirt release.

Currently handles:

- KubeVirt (CRDs separated out to dedicated Chart)
- KubeVirt CDI (CRDs separated out to dedicated Chart)

# Usage

1. Create namespace for KubeVirt, with proper labels. To get right labels, consult the [KubeVirt release manifests](https://github.com/kubevirt/kubevirt/releases)

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

2. 
```bash
TODO 
```


TODO:
- template for resource names
- add templates for kubevirt cr
- add logic to create pr 
- values can't be hardcoded but moved from downloaded manifests
