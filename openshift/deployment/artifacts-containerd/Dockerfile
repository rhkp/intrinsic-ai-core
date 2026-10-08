FROM docker.io/library/alpine:3.22

# The upstream artifact service always creates a containerd client, even when
# its local image-registry endpoint is disabled. OpenShift supplies a
# pod-local, unprivileged containerd API endpoint for that client.
RUN apk add --no-cache containerd=2.1.5-r2

COPY containerd.toml.in /etc/containerd/containerd.toml.in
COPY entrypoint.sh /usr/local/bin/containerd-entrypoint
RUN chmod 0555 /usr/local/bin/containerd-entrypoint

USER 1001
ENTRYPOINT ["/usr/local/bin/containerd-entrypoint"]
