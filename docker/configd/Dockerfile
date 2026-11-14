# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# carbonio-configd image: the static configd binary plus the configuration
# templates of the carbonio-core package, and nothing else (about 15-20 MB).
#
# Use it as a container (the pod runs configd alone), or as a layer:
#   COPY --from=<registry>/carbonio-configd:main /opt/zextras/ /opt/zextras/
# into an image whose configd needs a co-located binary (postconf, configd ldap).
#
# Postfix configuration (CO-4621): /opt/zextras/common/conf is root-owned and configd
# never writes it. A change (postconf -e/-X, master.cf and tag_as_*.re renders) is
# staged in /opt/zextras/data/postfix/conf-staging (0700, owned by uid 999, created
# here) and applied by the root helper /opt/zextras/libexec/postfix-conf
# (root:root 0755, shipped here). configd runs the helper directly when it is uid 0
# and through `sudo -n` otherwise. This image has no sudo and no systemd and runs
# as uid 999, so as shipped a Postfix configuration change FAILS with an error that
# names the helper: it never silently skips and never writes common/conf itself.
# To let configd manage Postfix configuration from this image, run the container as
# root (--user 0 / runAsUser: 0). A failed run discards the staged change, so a
# separate process cannot pick it up later. The helper creates its lock directory
# /run/postfix-conf itself (/run is created here).
#
# Build from the repository root:
#   docker build -f docker/configd/Dockerfile .

# Static binary, built for the target platform from this checkout.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/configd ./cmd/configd \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/postfix-conf ./cmd/postfix-conf

# Templates, extracted from the Carbonio debs of the same release: carbonio-core
# (zmconfigd.cf and the conf/*.in templates) and carbonio-common-appserver-conf
# (the LDAP attrs schema, conf/attrs, which carbonio-core does not ship).
# Both are Architecture: all, so they are fetched once on the build platform
# for every target.
FROM --platform=$BUILDPLATFORM docker.io/library/ubuntu:resolute AS installer

# Reproducible template source. The Zextras apt repository is not signed
# (it publishes no InRelease/Release.gpg, so there is no key to verify it
# with, and `[trusted=yes]` is the only way apt accepts it). Compensate by
# pinning what is downloaded:
#   CARBONIO_VERSION          exact version of both debs (apt-get download
#                             pkg=version); empty means the newest of the repo.
#   CARBONIO_CORE_SHA256 / CARBONIO_APPSERVER_CONF_SHA256
#                             expected SHA-256 of each deb; when set, the build
#                             fails on a mismatch. Release builds MUST set all
#                             three.
ARG CARBONIO_VERSION=
ARG CARBONIO_CORE_SHA256=
ARG CARBONIO_APPSERVER_CONF_SHA256=

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && printf 'deb [trusted=yes] https://repo.zextras.tools/repository/ubuntu-devel-jammy jammy main\n' \
    > /etc/apt/sources.list.d/zextras.list \
 && apt-get update \
 && mkdir -p /core /tmp/debs && cd /tmp/debs \
 && pin="${CARBONIO_VERSION:+=${CARBONIO_VERSION}}" \
 && apt-get download "carbonio-core${pin}" "carbonio-common-appserver-conf${pin}" \
 && check() { \
      [ -z "$2" ] || echo "$2  $(ls "$1"_*.deb)" | sha256sum -c -; \
    } \
 && check carbonio-core "${CARBONIO_CORE_SHA256}" \
 && check carbonio-common-appserver-conf "${CARBONIO_APPSERVER_CONF_SHA256}" \
 && for deb in /tmp/debs/*.deb; do dpkg -x "$deb" /core; done \
 && rm -rf /tmp/debs

# The layout configd reads with CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd.
RUN mkdir -p /staging/opt/zextras/share/configd/conf/zmconfigd /staging/etc \
 && install -d -m 1777 /staging/tmp \
 && install -d -m 0755 /staging/run \
 && install -d -m 0700 -o 999 -g 999 /staging/opt/zextras/data/postfix/conf-staging \
 && cd /core/opt/zextras/conf \
 && cp zmconfigd.cf *.in /staging/opt/zextras/share/configd/conf/ \
 && cp zmconfigd/*.cf /staging/opt/zextras/share/configd/conf/zmconfigd/ \
 && cp -r attrs /staging/opt/zextras/share/configd/conf/attrs \
 && printf 'zextras:x:999:999:Carbonio:/opt/zextras:/usr/sbin/nologin\n' > /staging/etc/passwd \
 && printf 'zextras:x:999:\n' > /staging/etc/group

FROM scratch

# OCI image metadata that never changes between builds. The build-specific
# labels (created, source, url, revision, ref.name, version) and the title,
# description and vendor come from the Jenkinsfile (dockerStage ocLabels), which
# passes them with --label; the same keys there override these.
LABEL org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.documentation="https://github.com/zextras/carbonio-configd/tree/main/docs"

# passwd, group, the templates, /tmp (rewrites render into a temporary file
# before they replace the target) and the Postfix staging directory, with their
# modes and owners.
COPY --from=installer /staging/ /
COPY --from=builder /out/configd /opt/zextras/bin/configd
# The root helper: root-owned, so the uid 999 daemon cannot replace it.
COPY --from=builder --chown=0:0 --chmod=0755 /out/postfix-conf /opt/zextras/libexec/postfix-conf

ENV CARBONIO_BASE_DIR=/opt/zextras \
    CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd

USER 999

ENTRYPOINT ["/opt/zextras/bin/configd"]
