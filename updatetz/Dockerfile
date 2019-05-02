FROM alpine AS cronjobber-updatetz

# Static labels
LABEL maintainer="Hidde Beydals <hello@hidde.co>" \
	  org.opencontainers.image.title="cronjobber-updatetz" \
	  org.opencontainers.image.description="Cronjobber is the Kubernetes cronjob controller patched with time zone support" \
	  org.opencontainers.image.url="https://github.com/hiddeco/cronjobber" \
	  org.opencontainers.image.source="git@github.com:hiddeco/cronjobber" \
	  org.opencontainers.image.vendor="Hidde Beydals" \
	  org.label-schema.schema-version="1.0" \
	  org.label-schema.name="cronjobber" \
	  org.label-schema.description="Cronjobber is the Kubernetes cronjob controller patched with time zone support" \
	  org.label-schema.url="https://github.com/hiddeco/cronjobber" \
	  org.label-schema.vcs-url="git@github.com:hiddeco/cronjobber" \
	  org.label-schema.vendor="Hidde Beydals"

COPY updatetz.sh /usr/local/bin
COPY docker-entrypoint.sh /usr/local/bin

WORKDIR /tmp
USER daemon

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]

# Dynamic labels
# Besides being informative these will also ensure each
# build ends up with a unique hash.
LABEL org.opencontainers.image.revision="$VCS_REF" \
	  org.opencontainers.image.created="$BUILD_DATE" \
	  org.label-schema.vcs-ref="$VCS_REF" \
	  org.label-schema.build-date="$BUILD_DATE"
