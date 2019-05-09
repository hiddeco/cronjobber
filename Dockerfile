ARG GO_VERSION=1.11

FROM golang:${GO_VERSION}-alpine AS builder

RUN mkdir /user \
    && echo 'daemon:x:2:2:daemon:/:' > /user/passwd \
    && echo 'daemon:x:2:' > /user/group

RUN apk add --no-cache ca-certificates git
RUN go get -u github.com/golang/dep/cmd/dep

WORKDIR ${GOPATH}/src/github.com/hiddeco/cronjobber

COPY ./Gopkg.toml ./Gopkg.lock ./
RUN dep ensure -vendor-only

COPY . ./

ARG VERSION
ARG VCS_REF

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w \
       -X github.com/hiddeco/cronjobber/pkg/version.VERSION=${VERSION} \
       -X github.com/hiddeco/cronjobber/pkg/version.REVISION=${VCS_REF}" \
       -a -installsuffix 'static' -o /cronjobber ./cmd/cronjobber/*

FROM scratch AS cronjobber

# Static labels
LABEL maintainer="Hidde Beydals <hello@hidde.co>" \
      org.opencontainers.image.title="cronjobber" \
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

COPY --from=builder /user/group /user/passwd /etc/
COPY --from=builder /cronjobber /cronjobber

USER daemon:daemon

ENTRYPOINT ["/cronjobber"]

ARG VCS_REF
ARG BUILD_DATE

# Dynamic labels
# Besides being informative these will also ensure each
# build ends up with a unique hash.
LABEL org.opencontainers.image.revision="$VCS_REF" \
      org.opencontainers.image.created="$BUILD_DATE" \
      org.label-schema.vcs-ref="$VCS_REF" \
      org.label-schema.build-date="$BUILD_DATE"
