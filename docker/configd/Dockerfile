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
    go build -trimpath -ldflags="-s -w" -o /out/configd ./cmd/configd

# Templates, extracted from the Carbonio debs of the same release: carbonio-core
# (zmconfigd.cf and the conf/*.in templates) and carbonio-common-appserver-conf
# (the LDAP attrs schema, conf/attrs, which carbonio-core does not ship).
# Both are Architecture: all, so they are fetched once on the build platform
# for every target.
FROM --platform=$BUILDPLATFORM docker.io/library/ubuntu:jammy AS installer

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
 && cd /core/opt/zextras/conf \
 && cp zmconfigd.cf *.in /staging/opt/zextras/share/configd/conf/ \
 && cp zmconfigd/*.cf /staging/opt/zextras/share/configd/conf/zmconfigd/ \
 && cp -r attrs /staging/opt/zextras/share/configd/conf/attrs \
 && printf 'zextras:x:999:999:Carbonio:/opt/zextras:/usr/sbin/nologin\n' > /staging/etc/passwd \
 && printf 'zextras:x:999:\n' > /staging/etc/group

FROM scratch

# passwd, group, the templates and /tmp (rewrites render into a temporary file
# before they replace the target), with their modes.
COPY --from=installer /staging/ /
COPY --from=builder /out/configd /opt/zextras/bin/configd

ENV CARBONIO_BASE_DIR=/opt/zextras \
    CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd

USER 999

ENTRYPOINT ["/opt/zextras/bin/configd"]
