# Derive only the pinned move-to-contact skill image. The first stage modifies
# the upstream skill source; the final stage copies that file onto a fresh
# upstream image so its entrypoint, command, user, and remaining layers stay
# unchanged.
FROM image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/ai.intrinsic.move_to_contact.move_to_contact_skill_image@sha256:0cfc737f4670c9b9b486ac6e2ec137b8ac552e32a98df5479f8ad929a9898a5b AS patched
USER 0
COPY patch_move_to_contact.py /tmp/patch_move_to_contact.py
RUN ["/skills/skill_service.runfiles/rules_python++python+python_3_11_x86_64-unknown-linux-gnu/bin/python3", "-S", "/tmp/patch_move_to_contact.py"]

FROM image-registry.openshift-image-registry.svc:5000/arhkp-intrinsic/ai.intrinsic.move_to_contact.move_to_contact_skill_image@sha256:0cfc737f4670c9b9b486ac6e2ec137b8ac552e32a98df5479f8ad929a9898a5b
COPY --from=patched /skills/skill_service.runfiles/intrinsic-core+/intrinsic/manipulation/skills/force/move_to_contact.py /skills/skill_service.runfiles/intrinsic-core+/intrinsic/manipulation/skills/force/move_to_contact.py
