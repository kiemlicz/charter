# Helm Chart generator

Create Helm Charts based on plain released manifests.  

Project originated from the lack of KubeVirt Helm Charts, which makes the process of upgrading harder.    
While waiting for [official ones](https://github.com/kubevirt/kubevirt/issues/8347) I've created simple app that auto creates the Charts on each
KubeVirt release.

The idea seems to be generic-enough so that I'll check if this can be used for any Helm Chart creation.

**WIP**  
**Charts not yet published until automated publisher is ready**

Currently, handles:

- KubeVirt (CRDs separated out to dedicated Chart)
- KubeVirt CDI (CRDs separated out to dedicated Chart)

# Usage

## KubeVirt

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
