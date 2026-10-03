# Intrinsic Core deployment notes

This repository records the working Ubuntu VM baseline and the active full
OpenShift migration pilot. Their deployment instructions are separate;
connection details, addresses, credentials, keys, certificates, authentication
codes, and raw system logs must stay out of this repository.

## Quick references

- [Deployed pods and their roles](PODS_README.md) — all 51 pods, grouped by
  namespace, with brief explanations and read-only inspection commands.
- [AWS simulation demo runbook](DEMO_README.md) — one-cycle commands, observed
  arm motion, recovery steps, and the current full-cycle limitation.
- [OpenShift and OpenShift AI work plan](OPENSHIFT_PLAN.md) — assessed migration
  blockers, component placement, and phased validation on a GPU-capable OpenShift cluster.
- [Full OpenShift pilot status](openshift/README.md) — dev01 preflight, recreated
  project, and current deployment gates.
- [Deployment approaches](approaches/README.md) — the **VM Only** baseline and
  archived **AWS VM + RHOAI** hybrid experiment.

## Authoritative instructions

Follow the upstream [Getting Started guide](https://github.com/intrinsic-ai/intrinsic-core/blob/main/developer_resources/learn/tutorials/getting_started.md).
The guide reviewed on 2026-09-29 specifies release `20260922.0` for both
`intrinsic-core` and `intrinsic-omts`. Recheck upstream instructions before a
future deployment; do not silently substitute a different release.

## Progress — 2026-09-29

**OMTS simulation deployment succeeded. The platform reported ready, all 51
Kubernetes pods were ready, the local asset API responded successfully, and
the NVIDIA GPU remained healthy. No GitHub login was used.**

1. Read the upstream guide and inspected the release's `setup_k3s.sh` and
   `setup_nvidia.sh` without executing them.
2. Connected with the user-provided SSH identity, strict host-key verification,
   batch authentication, and agent forwarding disabled. The existing trusted
   host-key entry was accepted. The private key stayed on the local machine.
3. Performed read-only prerequisite checks:

   | Check | Observed result |
   | --- | --- |
   | Operating system | Ubuntu 26.04 LTS |
   | Architecture | x86_64 |
   | vCPUs | 8 |
   | Memory | Approximately 30 GiB usable; no swap |
   | Root filesystem | Approximately 482 GiB available |
   | Temporary filesystem | Separate 16 GiB filesystem |
   | GPU | NVIDIA Tesla T4 detected on PCI bus |
   | GPU driver tooling | `nvidia-smi` absent; GPU operation unverified |
   | Privilege access | Noninteractive sudo available |
   | Existing installation | No Core/OMTS checkout or k3s configuration found at the checked paths |

   These initial checks did not establish full compatibility or performance.
   Usable RAM was approximately 30 GiB versus the guide's stated 32 GiB minimum;
   GPU operation was subsequently validated in step 10. Build concurrency and
   temporary storage require attention.
4. Completed installation of the guide's initial prerequisites in its documented
   order: `apt update`, installation of `git` and `git-lfs`, `git lfs install`,
   and installation of `gh`. Verified Git 2.53.0, Git LFS 3.7.1, and GitHub CLI
   2.46.0. The noninteractive authentication check confirmed that `gh` is not
   signed in. That does not prevent anonymous access to public repositories
   and release assets.
5. Investigated an alternative to the guide's GitHub CLI authentication step.
   Unauthenticated requests from the VM successfully resolved the `20260922.0`
   tags for both repositories. Anonymous HEAD requests for both required Linux
   release assets returned HTTP 200.
6. Completed anonymous acquisition directly on the VM, as selected by the user.
   Both pinned clones passed `git lfs pull`, `git lfs fsck`, and working-tree/index
   diff checks. GitHub login was skipped; no GitHub credentials were supplied.

   | Repository | Checked-out commit | LFS files |
   | --- | --- | --- |
   | `intrinsic-core` | `523f2e03ddf3cb46a19b15be5e81586ae2f538c2` | 432 |
   | `intrinsic-omts` | `8253cdfdd173da9d5b7c9bb7b8cee817f66902ae` | 6 |

7. Downloaded `intrinsic-base-linux-amd64.tar` (1,394,862,080 bytes) and
   `inctl-linux-amd64` (110,765,848 bytes) into `/tmp` on the VM. Both SHA-256
   checks passed.
8. Ran the pinned `~/intrinsic-core/intrinsic_runtime/setup_k3s.sh`. The first
   attempt stopped because the agent's restrictive installation umask left the
   root-owned public Helm executable at mode `700`. Corrected that binary to
   `755`, normalized the extracted k9s executable to root ownership, and reran
   the unchanged script with umask `022`. Log files remain private; the user
   kubeconfig was verified at mode `600`. Setup completed and k3s is active.
9. A fresh SSH session confirmed membership in `containerd`, providing the group
   access that the guide obtains with `newgrp containerd`. Checked the Core
   archive for unsafe paths and entry types, extracted it into `/tmp`, and ran
   `/tmp/intrinsic-base`. Deployment succeeded; all 17 Core pods reported ready.
   Installed the verified CLI as root-owned `/usr/local/bin/inctl`, mode `755`.
10. Ran the unchanged upstream `setup_nvidia.sh` with sudo. NVIDIA 595.91.07
    compiled for the running AWS kernel and all driver/toolkit packages finished
    installing. GPU readiness was blocked because `nouveau` already owned the
    T4. Verified the installed Nouveau blacklist and updated initramfs, then
    initiated one reboot to activate the new driver. SSH initially timed out,
    then recovered with strict host-key verification. The T4 reports driver
    595.91.07 and 15,360 MiB memory. The node and NVIDIA plugin are ready;
    Kubernetes reports the script's configured 48 shared GPU slots (one physical
    GPU). All 17 Core pods recovered and `inctl asset list --address
    localhost:17080` succeeded. No AWS network rules were changed by the agent.
11. Installed Bazelisk `v1.29.0`, resolved from the guide's latest-release source,
    after checking its GitHub-provided SHA-256:
    `5a408715e932c0250d28bd84555f12edbf70117de42f9181691c736eacc4a992`.
    Created the guide's `/usr/bin/bazel` and libxml compatibility symlinks after
    verifying destination paths were absent and the library target existed.
    Prepared private `~/tmp` storage on the root filesystem for the build.
12. Started the OMTS build/deployment as the unprivileged `ubuntu` user in
    transient systemd service `intrinsic-omts-build.service`. This keeps the job
    running across SSH disconnections. The log is private at
    `~/intrinsic-install-logs/omts-build.log`. The effective build command is:

    ```sh
    cd ~/intrinsic-omts
    TMPDIR="$HOME/tmp" bazel --batch run //:omts_solution \
      --config=lab_bb_01 --jobs=4 -- \
      --address localhost:17080 --operation_mode=sim
    ```

    Anonymous Git environment settings above are also applied to the service.
    The guide's build and deployment target is unchanged; batch mode avoids a
    persistent Bazel server and four jobs limit concurrent build work.
13. The first build stopped during analysis: `tinygltf` 2.9.6 archives from both
    GitHub and Intrinsic's mirror no longer matched the original Bazel registry
    checksum. The Bazel registry has a
    [documented archive-hash correction](https://github.com/bazelbuild/bazel-central-registry/commit/419b9bda0dc478024b66ece0ac0dba939576eb1e).
    Recovered the original archive from
    [MacPorts' retained distfile](https://distfiles.macports.org/tinygltf/tinygltf-2.9.6.tar.gz)
    and verified it against the **original** pinned SHA-256:
    `ba2c47a095136bfc8a5d085421e60eb8e8df3bca4ae36eb395084c1b264c6927`.
    Saved it as `~/intrinsic-distfiles/v2.9.6.tar.gz`. No dependency version or
    expected checksum was changed, and no unexpected archive was accepted.
14. Started a second build as `intrinsic-omts-build-2.service`, with private log
    `~/intrinsic-install-logs/omts-build-2.log`. Added
    `--distdir=/home/ubuntu/intrinsic-distfiles` before the command's `--` separator
    so Bazel can use that verified local archive. Dependency analysis completed
    and compilation proceeded without another fetch failure.
15. After repeated checks showed approximately 24 GiB of available memory and
    all 17 Core pods ready during compilation, stopped the four-job build and
    resumed from its existing cache with eight jobs in
    `intrinsic-omts-build-3.service`. The build service has `MemoryHigh=24G`,
    `MemoryMax=26G`, and `MemorySwapMax=0` to leave memory for the runtime.
    Its private log is `~/intrinsic-install-logs/omts-build-3.log`. Dependency
    versions, the local verified distfile, and simulation flags are unchanged.
    This restart was a resource adjustment, not a new build failure.

    The current effective command, run as the unprivileged VM user, is:

    ```sh
    cd ~/intrinsic-omts
    TMPDIR="$HOME/tmp" bazel --batch run //:omts_solution \
      --config=lab_bb_01 --jobs=8 \
      --distdir="$HOME/intrinsic-distfiles" -- \
      --address localhost:17080 --operation_mode=sim
    ```

    The service also receives the anonymous Git environment settings documented
    below. It survives SSH disconnections, but is a transient service and is not
    configured to resume automatically after a VM reboot.
16. The eight-job build completed successfully after approximately 141 minutes
    in that invocation, reusing prior cached work. Bazel reported 23,934 executed
    actions in the successful invocation. The same command then started
    processing deployment assets and uploading container layers into the local
    runtime. All 17 Core pods remained ready.
17. Deployment completed successfully in simulation mode. The command reported
    743.38 seconds to process assets, 5.60 seconds to deploy the application, and
    38.41 seconds to wait for readiness. The systemd service exited with status
    `0` and `Result=success`.
18. Final read-only verification passed:

    | Check | Result |
    | --- | --- |
    | Core pods | 17/17 ready |
    | Application pods | 4/4 ready |
    | Resource pods | 13/13 ready |
    | Skill pods | 8/8 ready |
    | All namespaces combined | 51/51 pods ready |
    | Local API | `inctl asset list --address localhost:17080` succeeded |
    | GPU | Tesla T4, driver 595.91.07, 835 MiB GPU memory in use |
    | Available host memory | Approximately 22 GiB |
    | Available root filesystem | Approximately 352 GiB |
    | GitHub authentication | No GitHub login file present; anonymous workflow retained |
    | Source changes | Core clean; OMTS only has the documented downloader patch |

    This verifies deployment and platform readiness. No robot motion or
    end-to-end task was commanded, and graphical visualization is not configured.

The reboot cleared the temporary `/tmp/intrinsic-base` installer. The installed
runtime and CLI persisted. If Core installation needs to be rerun later,
re-download and verify its pinned release archive before extracting it again.

## Authentication-free source acquisition

The guide uses `gh auth login`, but this is not an intrinsic runtime requirement.
The user approved replacing `gh repo clone` with HTTPS `git clone` at the same
release tag and replacing `gh release download` with anonymous HTTPS downloads
of the same release assets. This deliberate acquisition-method variation retains
the guide's versions and deployment steps. No transfer from the Mac is needed.

Commands used for source acquisition, with private log redirection omitted:

```sh
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_COUNT=2
export GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=
export GIT_CONFIG_KEY_1=http.extraHeader GIT_CONFIG_VALUE_1=
git clone --revision=20260922.0 https://github.com/intrinsic-ai/intrinsic-core.git ~/intrinsic-core
git clone --revision=20260922.0 https://github.com/intrinsic-ai/intrinsic-omts.git ~/intrinsic-omts
# Run in each checkout:
git lfs pull
git lfs fsck
git diff --exit-code
git diff --cached --exit-code
```

The acquisition shell disables credential helpers, custom HTTP authorization
headers, and interactive Git credential prompts. Existing destination paths are
checked before writing; they are not overwritten.

Release files are streamed with Python's HTTPS client from
`https://github.com/intrinsic-ai/intrinsic-core/releases/download/20260922.0/`
into `/tmp/<asset>.part`. Each file is promoted to `/tmp/<asset>` only after its
SHA-256 matches the digest retrieved from GitHub release metadata:

| Asset | Expected SHA-256 |
| --- | --- |
| `intrinsic-base-linux-amd64.tar` | `56cbe2e400e83aff80c3635565e31d3db847a130baca2107a26eaa9f8f3051e2` |
| `inctl-linux-amd64` | `51c9eb0edc7b3ca1a7536a6937f28096920336f39ec6ace9311ca336514ff93b` |

These hashes check download integrity against GitHub's release metadata; they
are not a separate publisher-signature verification. Downloads are for Linux
amd64, matching the VM.

### OMTS build-time download correction

The pinned OMTS source contains a `bazel/gh_release.bzl` rule that invokes `gh`
and assumes its repository is private. Its five required release assets were
individually verified accessible anonymously. Replaced that invocation with
Bazel's HTTPS downloader, retaining the pinned asset names, release versions,
SHA-256 verification, extraction behavior, and build targets. The replacement
requires an exact filename and a nonempty checksum.

The exact VM source change is saved in
[`patches/omts-anonymous-releases.patch`](patches/omts-anonymous-releases.patch).
It applies to the pinned OMTS checkout and avoids adding credentials or replacing
the system `gh` executable. The initial checkout was clean; this patch is the
intentional local source modification. All five release repositories materialized
successfully during the second build with checksum enforcement. The subsequent
full build and simulation deployment succeeded, including the platform's
readiness wait.

Installation output is retained only on the VM under
`~/intrinsic-install-logs/`, using restrictive creation permissions. It is not
copied into this repository.

## Network access review

The user confirmed that only SSH port 22 is allowed inbound. The supplied rule
allows SSH from any IPv4 source; restricting that source to the user's current
address or trusted VPN range was recommended. That restriction has not been
verified. No AWS rules were changed by the agent.

A read-only `sshd -T` check reported public-key authentication enabled and both
password and keyboard-interactive authentication disabled. Root login was
`prohibit-password`. These default server settings do not replace restricting
the security group's SSH source; no SSH configuration was changed.

Runtime setup can proceed based on the user's confirmation that other inbound
ports are closed across the attached security groups. The setup script configures
a Kubernetes LoadBalancer with ports 80, 17080, and 7447, and k3s runs its control
plane. Keep those services inaccessible from the public internet. Use SSH
tunneling for remote application access. AWS rules were reported by the user,
not independently inspected from this workspace.

## Deployment complete

The Getting Started build, simulation deployment, and readiness checks are
complete. The build used the guide's `lab_bb_01` configuration with eight build
jobs, a service memory limit, and `TMPDIR=~/tmp`, following the guide's
temporary-storage workaround because the separate `/tmp` filesystem is only
16 GiB. The next tutorial covers visualization, which remains optional work.

Review existing paths before any overwrite or symlink operation. Use the
guide's documented disk-space workaround if temporary storage becomes a
constraint. Do not run destructive uninstall/reset troubleshooting on an
existing deployment without assessing its data and workloads.

## Handling sensitive information

Use the supplied connection details privately. Documentation examples must use
placeholders such as `<SSH_KEY_PATH>` and `<VM_HOST>`. Do not disable SSH/TLS
verification, forward the SSH agent, export credentials, commit kubeconfigs, or
copy raw logs into Git. Share only selected, sanitized health results.

## Later visualization access (not yet configured)

The upstream [visualization guide](https://github.com/intrinsic-ai/intrinsic-core/blob/main/developer_resources/learn/tutorials/visualize_the_robot.md)
uses RViz or Gazebo in a graphical environment. For RViz on a separate machine,
it describes tunneling ports 7447 and 17080. A loopback-only tunnel can keep the
VM's inbound rules limited to SSH:

```sh
ssh -a -N -i "<SSH_KEY_PATH>" \
  -o StrictHostKeyChecking=yes -o ExitOnForwardFailure=yes \
  -L localhost:17080:localhost:17080 \
  -L localhost:7447:localhost:7447 "ubuntu@<VM_HOST>"
```

Replace placeholders privately. This tunnel has not been opened, and no
graphical environment or visualization client has been installed. The tunnel
alone does not provide a GUI; a compatible visualization environment is also
needed.

### Visualization review — 2026-09-29

Checked current upstream documentation against the pinned visualization guide.
The documented viewers are RViz (belief state) and Gazebo (physics state).
The server's loopback endpoints on ports 7447 and 17080 were reachable.
No host-side ROS, RViz, Gazebo GUI, Xfce, or TigerVNC command was installed.

For viewing from a Mac browser, the proposed approach is a graphical session on
the existing VM, exposed through TigerVNC and
[noVNC](https://github.com/novnc/noVNC), with both services bound to loopback and
access carried over SSH. Xfce, TigerVNC, noVNC, websockify, and Mesa diagnostic
packages have candidates in the VM's configured Ubuntu repositories. This is
an additional remote-desktop layer, not a built-in Intrinsic web interface.
Package availability does not yet verify RViz/Gazebo rendering in that session;
OpenGL operation must be checked during setup. No packages were installed and
no desktop listener or tunnel was started during this review.

With that approach, only a chosen noVNC browser port (for example 6080) needs
forwarding to the Mac; the viewer connects to Core locally on the VM. The
alternative is RViz on a separate Linux graphical environment, using the
7447/17080 tunnel above. Neither approach requires additional AWS inbound rules.

After visualization is working, upstream's
[Jog the robot](https://github.com/intrinsic-ai/intrinsic-core/blob/main/developer_resources/learn/tutorials/jog_the_robot.md)
tutorial provides an interactive demo. The subsequent
[Visualize the Solution](https://github.com/intrinsic-ai/intrinsic-core/blob/main/developer_resources/learn/tutorials/visualize_the_solution.md)
tutorial registers the workpiece pose estimator and runs `//src:omts_app` with
`configs/lab_bb_01/app_config.yaml`, matching this deployment. Neither demo was
run during the visualization review.

## Stop overnight and resume

Use the EC2 console's **Stop instance** action and later **Start instance** on
the same instance. Do not terminate it or replace it with a newly launched VM.
[AWS documents](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/how-ec2-instance-stop-start-works.html)
that EBS data and the private address persist across stop/start, while an
automatically assigned public address usually changes. RAM and instance-store
data do not persist; EBS storage continues to incur charges while stopped.

Read-only checks confirmed that source checkouts, the Bazel cache, k3s data,
and all four application persistent volumes use the EBS root filesystem.
The VM also has an instance-store disk, but the checked persistent application
data does not reside there. The `k3s` service is enabled at boot.

After starting, use the new public address privately for SSH and any tunnels,
with the same SSH identity and verification against the previously trusted host
key. Check GPU, pod, and local API readiness before continuing visualization.
The installed system and cached builds should not require a fresh installation
or full rebuild. Live simulation state in RAM and temporary files may reset;
application recovery still needs to be verified after the actual stop/start.
No stop or shutdown command was issued during this review.

## Resume and viewer setup — 2026-09-30

Reconnected at the user-provided replacement address with strict host-key
checking against the previously trusted host entry, using `HostKeyAlias`.
Connection addresses and the AWS allocation identifier are not stored here.
The checkouts, build cache, and application data survived the stop/start.
Core's API responded and ICON reported `ENABLED`.

The NVIDIA driver worked, but the device plugin repeatedly failed to initialize
inotify, leaving inference unscheduled. The host's
`fs.inotify.max_user_instances` limit was 128 and root processes had 128 inotify
file-descriptor references. Added `/etc/sysctl.d/90-intrinsic-inotify.conf` with
`fs.inotify.max_user_instances = 1024`, applied that one setting, and recreated
the failed NVIDIA plugin pod. Its rollout succeeded and the inference pod
became ready. This additional host resource-limit fix is separate from the
upstream installer and persists across boots.

Following the ROS installation instructions linked by Intrinsic, downloaded
the official `ros2-apt-source_1.3.0.resolute_all.deb` anonymously and verified its
GitHub release SHA-256:
`e70bc980395f4a3b366b9c5e7f8f9b4d63a4064fb115f6ff3bc52f7faa02ef69`.
Installed the repository configuration and refreshed signed APT metadata.
The reviewed viewer package plan adds 1,245 packages, downloads approximately
791 MB, and uses approximately 3.5 GB of disk, without upgrading or removing
existing packages. It includes `ros-lyrical-desktop`,
`ros-lyrical-rmw-zenoh-cpp`, Xfce, TigerVNC, noVNC, websockify, Mesa diagnostics,
and D-Bus X11 support, with optional recommended packages omitted.

Viewer installation completed successfully. The live workcell was visually
verified in RViz through noVNC, and all 51 Kubernetes pods were ready afterward.
The GPU plugin again exposes 48 shared slots for the single T4.
The installed service files are in [`viewer/`](viewer/). VNC and the browser
proxy use owner-only Unix sockets inside a private runtime directory,
accessed via SSH. No separate VNC password or public desktop port is needed.
An X authority cookie is generated privately on the VM; it is not stored in
this repository. The session is limited to 3 GiB RAM and two CPU cores' worth
of CPU time, with software rendering for RViz. Simulation inference continues
to use the NVIDIA GPU.

### Open the viewer

The viewer is available on the Mac at
[Open RViz](http://localhost:6080/vnc.html?autoconnect=true&resize=scale)
while the SSH tunnel is running. A tunnel was started during setup and the
browser tab was opened. This is a live view of the software's workcell state;
no jogging or machine-tending process was started during viewer setup.

For a later session, start the VM and run this from the local repository:

```sh
bash viewer/connect.sh "<VM_HOST>" "<PREVIOUS_TRUSTED_HOST>" "<SSH_KEY_PATH>"
```

Replace placeholders privately. The second argument identifies the original
trusted entry in the local SSH known-hosts file, allowing the same host key to
be verified even when the public address changes. Keep the terminal open while
viewing; Ctrl+C closes this foreground tunnel. If a setup-created tunnel is
already listening on local port 6080, use the existing viewer connection.

The VM's `intrinsic-viewer.service` starts on boot and can be managed over SSH:

```sh
systemctl status intrinsic-viewer
sudo systemctl restart intrinsic-viewer
sudo systemctl stop intrinsic-viewer
```

Stopping the viewer does not stop Core or the simulation. VNC uses
`/run/intrinsic-viewer/vnc.sock`, and websockify uses
`/run/intrinsic-viewer/web.sock`; both have mode `600`, inside a directory with
mode `700`. Neither service listens on TCP on the VM. The Mac tunnel listens
only on loopback. AWS inbound rules were not changed.

RViz uses software OpenGL 4.5 via Mesa llvmpipe, fixed frame `root`, and a
transient-local subscription to `/workcell_markers`. The desktop stays at
1600×900 and noVNC scales it to the browser; opening the link in a larger browser
window makes the controls easier to read. RViz's view controls rotate, pan, and
zoom the camera without commanding robot motion. Private diagnostic logs remain
under `~/intrinsic-install-logs/viewer-*.log` on the VM.

## Simulation demo attempts — 2026-09-30

Followed the pinned upstream **Visualize the Solution** tutorial. Verified that
the deployment's `simulated` setting was true, the UR and Hand-E resources used
simulation images, all 51 pods were ready, and ICON was enabled.

Workpiece pose-estimator registration succeeded with the tutorial's parameters.
Built and ran `//src:omts_app` with `configs/lab_bb_01/app_config.yaml` and an
explicit `--num_cycles=1` limit. The first attempt failed when the simulated
gripper's Zenoh query received no reply. Router logs showed no matching gripper
queryable even though the driver pod was ready. Recreated only
`rs-hande-gripper-0` and verified that its command handler registered with Zenoh.

The retry executed camera capture, pose estimation, arm motion, gripping,
placement, and unloading steps. It then failed planning a motion after reattaching
the workpiece during unloading, reporting a collision between
`raw_stock_2x3x5.base_link` and `enclosure.base_link`. A clean retry after the
upstream-documented world reset reproduced the same collision. This resembles
the tutorial's documented simulation placement/grasp limitation; the exact cause
of our collision has not been established.

**Functional simulation steps were demonstrated, but a complete machine-tending
cycle has not passed.** Collision checking and upstream motion code were unchanged.
After the attempts, reset the world again and re-enabled the simulated controller.
ICON reported enabled, all 51 pods were ready, and no demo was left running.
Private registration/demo logs remain on the VM with mode `600`.

See [the demo runbook](DEMO_README.md) for repeatable commands and the distinction
between demonstrated behavior and the remaining simulation failure. The
[OpenShift plan](OPENSHIFT_PLAN.md) now uses this more precise baseline.

## Hybrid feasibility experiment — 2026-10-01

We tested RHOAI-managed Triton serving on dev01 while Intrinsic Core and the
simulation remained on the AWS VM. Internal gRPC inference passed, but the
AWS VM could not complete gRPC inference through the token-authenticated model
route because HTTP/2 (`h2`) was not negotiated. A manual custom-certificate
change to the RHOAI-managed route was not a durable, supported configuration;
REST would require client/integration changes and was not validated end to end.
We have set the hybrid approach aside and will plan a full OpenShift deployment.
See the [archived experiment record](approaches/tried-not-feasible-aws-vm-with-rhoai/README.md)
and [full OpenShift work plan](OPENSHIFT_PLAN.md).
