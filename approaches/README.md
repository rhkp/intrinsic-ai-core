# Intrinsic deployment approaches

This directory keeps deployment experiments distinct. The VM deployment remains
the known baseline. The AWS VM + RHOAI hybrid experiment is archived; the current
direction is a full OpenShift deployment.

| Approach | Placement | Status | Reference |
| --- | --- | --- | --- |
| **VM Only** | Intrinsic Core, OMTS, simulation, and inference run on the AWS VM's K3s cluster. | Deployed and used as the control case; current demo limitations are documented. | [VM Only reference](vm-only/README.md) |
| **AWS VM + RHOAI (Hybrid)** | Intrinsic execution and simulation on the AWS VM; RHOAI-managed Triton serving on OpenShift. | Tried and not pursued. Internal gRPC inference worked on dev01, but the AWS VM could not complete external gRPC inference through the token-authenticated route because HTTP/2 (`h2`) was not negotiated. REST/client adaptation and a supported durable custom-certificate route setup were not established. | [Archived hybrid record](tried-not-feasible-aws-vm-with-rhoai/README.md) · [OpenShift notes](tried-not-feasible-aws-vm-with-rhoai/openshift/README.md) · [test record](tried-not-feasible-aws-vm-with-rhoai/TEST_PLAN.md) |

The [full OpenShift work plan](../OPENSHIFT_PLAN.md) is the active planning
document. Hybrid notes are retained only as a record of the approach tested and
the reason it was set aside; they are not current deployment instructions.

## Experiment rules

- Keep the VM Only deployment and its runbook as the comparison baseline.
- Keep historical hybrid manifests and test records under
  `tried-not-feasible-aws-vm-with-rhoai/`; do not use them as active deployment
  instructions.
- Record software/model versions and test outcomes, but keep endpoint addresses,
  kubeconfigs, tokens, keys, certificates, personal data, and raw sensitive logs
  out of Git.
- Treat each stage as a gate. Do not direct the OMTS behavior tree to remote
  or alternate inference until the endpoint, request contract, failure behavior,
  and output have passed the earlier checks.
