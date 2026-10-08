# ResourceRegistry OpenShift image

The pinned upstream Core ResourceRegistry service renders ResourceSets. The
OpenShift-only GPU toleration patch therefore has to be present in this service
image; patching the OMTS CLI alone does not affect generated PodSpecs.

The Dockerfile layers the patched upstream binary over the already verified
OpenShift ResourceRegistry image. This keeps its runtime, entrypoint, arguments,
and security settings unchanged.

Run from the repository root after the OMTS workspace has the pinned Core patch
set prepared:

```sh
bash openshift/deployment/resource-registry/build-image.sh
```

The script builds the binary from pinned Core in the persistent Bazel workspace,
transfers it in bounded chunks, checks the transfer SHA256, then uses an
OpenShift Binary Build to publish the image. It prints the immutable project
registry reference to use in the controller image override and image lock.
