# OpenShift configuration audit

> [!NOTE]
> This project was developed with assistance from AI tools.

This directory contains offline tooling that inventories target-configuration
and image-identity surfaces without contacting a cluster.

Run the audit from the repository root:

```sh
python3 openshift/audit/audit_config.py
```

Use `--strict` in CI or pre-deployment checks to fail when controller image
constants or generated Quay image values drift from `openshift/image-lock.json`.

The audit intentionally reports configuration locations and counts, not live
cluster values. Keep environment-specific observations, inventories, and
credentials out of this directory and out of Git.
