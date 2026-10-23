# Intrinsic deployment approaches
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


This directory keeps deployment experiments distinct. The VM deployment remains
the known baseline. The AWS VM + RHOAI hybrid experiment reopened on 2026-10-22
to resolve external gRPC connectivity. The full OpenShift deployment remains
available as a separate validated topology.

| Approach | Placement | Status | Reference |
| --- | --- | --- | --- |
| **VM Only** | Intrinsic Core, OMTS, simulation, and inference run on the AWS VM's K3s cluster. | Deployed and used as the control case; current demo limitations are documented. | [VM Only reference](vm-only/README.md) |
| **AWS VM + RHOAI (Hybrid)** | Intrinsic execution and simulation on the AWS VM; RHOAI-managed Triton serving on OpenShift. | Verified 2026-10-23: two complete simulation cycles through a token-authenticated RHOAI gRPC Route. Both remote models served actual demo requests; all five stages succeeded. Native rollback exercised; existing sources and build caches preserved. | [Verified hybrid demo](aws-vm-with-rhoai/README.md) |

The [full OpenShift work plan](../OPENSHIFT_PLAN.md) tracks that deployment.
The current hybrid code, configuration and verification live under
`aws-vm-with-rhoai/`. Its README contains the runbook and original failure diagnosis;
obsolete experiment files are recoverable from Git history.

## Experiment rules

- Keep the VM Only deployment and its runbook as the comparison baseline.
- Keep hybrid code, configuration examples and evidence under `aws-vm-with-rhoai/`.
  Its README is the single runbook; retain earlier attempts in Git history.
- Record software/model versions and test outcomes, but keep endpoint addresses,
  kubeconfigs, tokens, keys, certificates, personal data, and raw sensitive logs
  out of Git.
- Treat each stage as a gate. Do not direct the OMTS behavior tree to remote
  or alternate inference until the endpoint, request contract, failure behavior,
  and output have passed the earlier checks.
