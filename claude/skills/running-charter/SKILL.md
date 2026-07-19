---
name: running-charter
description: Runs the charter tool to update and publish Helm charts. Use when new dry-running changes are needed.
---

Build the app with `go build ./cmd/updater/`

**Run update mode (dry-run, no git ops)**
Modify `charts/**/Chart.yaml` `appVersion` to previous version prior to running this, otherwise logic might stop the update as the version
is the same as remote

``` 
./updater --mode update --offline
```

Full update mode requires GH_TOKEN, ask user before running

```
GH_TOKEN=<token> ./updater --mode update
```

**Run publish mode**

```
./updater --mode publish
```
