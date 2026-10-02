# Hybrid experiment implementation layout

This is the implementation structure for the separate AWS VM + RHOAI track.
The OpenShift inventory and project bootstrap live under `openshift/`. The
CIFAR10 smoke manifest and authenticated external-route test are complete;
production-model manifests remain gated until model artifacts, storage, quota,
GPU execution, and the Intrinsic request contract are validated.

```text
approaches/
├── README.md
├── vm-only/
│   └── README.md                         # links to the current baseline records
└── aws-vm-with-rhoai/
    ├── README.md                         # topology and placement proposal
    ├── TEST_PLAN.md                      # gates, pass/stop criteria, rollback
    ├── IMPLEMENTATION_LAYOUT.md          # this file
    ├── openshift/                        # RHOAI target-specific implementation
    │   ├── README.md                     # verified versions, workflow, serving gates
    │   ├── common.py                     # quiet context-resolution helpers
    │   ├── preflight.py                  # read-only sanitized inventory
    │   ├── bootstrap_project.py          # creates only arhkp-intrinsic with --apply
    │   └── serving/
    │       └── README.md                 # serving prerequisites and ownership
    ├── vm-client/                        # future resource endpoint config / adapter if needed
    │   ├── README.md                     # contract and safe invocation
    │   └── examples/                     # sanitized config templates only
    ├── tests/                            # deterministic synthetic contract tests
    └── results/                          # sanitized comparison records only
```

The RHOAI operator and platform services are cluster-owned, so this area
contains only project-scoped resources owned by the pilot plus read-only
preflight tooling. The bootstrap script intentionally creates no quota,
policy, storage, runtime, model, or endpoint. Add version-validated serving
manifests only after their concrete inputs and owners are agreed.

## Ownership boundary

- `openshift/serving/` will describe only the model-serving workload and the
  narrow policies/storage references it needs. It must not contain a raw Secret
  value or cluster-admin credential.
- `vm-client/` will hold any adapter code and sanitized examples. Actual endpoint
  values and trust/credential files are injected at runtime by the approved
  configuration and secret mechanism; local `.env` files, kubeconfigs, private
  keys, certificates, and tokens stay out of Git.
- `tests/` will contain repeatable tests using generated or approved synthetic
  inputs. Avoid checking in production/customer images, datasets, or full
  request/response traces.
- `results/` will contain only sanitized measurements and findings. Put raw logs
  and restricted evidence in an approved access-controlled system.

The `openshift` and `vm-client` paths remain independent so each side can be
tested, reviewed, and reverted separately. Create a dedicated Git branch
before adding runtime/client code or serving manifests; do not fold those into
the VM Only setup until the comparison passes and a later migration decision
is made.
