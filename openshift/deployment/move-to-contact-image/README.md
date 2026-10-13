# OpenShift move-to-contact image
> [!NOTE]
> This project was developed with assistance from AI tools.

> Deployment examples use generic placeholders for cluster context, project namespace, registry, storage class, and GPU settings. Replace them with approved environment values before use; scripts may read `OPENSHIFT_CONTEXT`, `OPENSHIFT_NAMESPACE`, `OPENSHIFT_API_SERVER_URL`, and `OPENSHIFT_GPU_TAINT_KEY` from the environment.


This derived image changes one upstream behavior: it gives the native PubSub
session an explicit Zenoh client configuration using
`INTRINSIC_ZENOH_ROUTER_ENDPOINT`. Upstream `MoveToContact` constructs
`pubsub.PubSub()` with no arguments; that binding defaults to the
`app-intrinsic-base` router, a namespace that is not present on the target cluster.

The patch uses the pinned upstream image digest as its base and copies only the
modified `move_to_contact.py` into a fresh copy of that image. This preserves
the upstream entrypoint, command, image user, and all other files. The Python
patch script fails if the pinned source no longer matches the reviewed import
and constructor lines.

Build it in the target project, without a VM:

```sh
oc -n your-project-namespace apply -f \
  openshift/deployment/manifests/move-to-contact-image-build.yaml
oc -n your-project-namespace start-build move-to-contact-openshift \
  --from-dir=openshift/deployment/move-to-contact-image --follow
oc -n your-project-namespace get istag move-to-contact-openshift:pilot \
  -o jsonpath='{.image.dockerImageReference}{"\n"}'
```

Record the resulting immutable project-registry digest in
`openshift/image-lock.json` and the controller's image override before
reconciling the skill ChartAssignment.
