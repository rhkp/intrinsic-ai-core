# VM Only approach

This is the current control case: Intrinsic Core and OMTS run together in K3s on
the GPU-equipped Ubuntu AWS VM. The existing root-level deployment journal,
pod reference, and simulation demo runbook are the detailed records; they remain
the source of truth rather than being copied here.

- [Deployment journal and setup commands](../../README.md)
- [Deployed pod roles](../../PODS_README.md)
- [Simulation demo and observed limitation](../../DEMO_README.md)

The baseline has shown perception and simulated arm motion, but it has not
completed a full machine-tending cycle: unloading encounters a repeatable
workpiece/enclosure collision. Record this limitation when comparing the hybrid
trial so a change in deployment placement is not confused with a change in
simulation behavior.

Do not add hybrid endpoint settings, OpenShift manifests, or RHOAI-specific
steps to this baseline. Keep connection details and sensitive artifacts outside
the repository.
