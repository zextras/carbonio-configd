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
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.26-bookworm AS builder

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
FROM --platform=$BUILDPLATFORM docker.io/library/ubuntu:jammy AS installer

ARG TARGETARCH
ARG BUILDARCH=amd64

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && if [ "${TARGETARCH}" != "${BUILDARCH}" ]; then \
      dpkg --add-architecture ${TARGETARCH} \
      && sed -i 's|^deb |deb [arch='"${BUILDARCH}"'] |' /etc/apt/sources.list \
      && printf 'Types: deb\nURIs: http://ports.ubuntu.com/ubuntu-ports/\nSuites: jammy jammy-updates jammy-security\nComponents: main restricted universe multiverse\nArchitectures: %s\nSigned-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg\n' "${TARGETARCH}" \
         > /etc/apt/sources.list.d/ports-${TARGETARCH}.sources; \
    fi \
 && printf 'deb [arch=%s trusted=yes] https://repo.zextras.tools/repository/ubuntu-devel-jammy jammy main\n' "${TARGETARCH}" \
    > /etc/apt/sources.list.d/zextras.list \
 && apt-get update \
 && mkdir -p /core /tmp/debs && cd /tmp/debs \
 && apt-get download carbonio-core:${TARGETARCH} carbonio-common-appserver-conf:${TARGETARCH} \
 && for deb in /tmp/debs/*.deb; do dpkg -x "$deb" /core; done \
 && rm -rf /tmp/debs

# The layout configd reads with CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd.
RUN mkdir -p /staging/opt/zextras/share/configd/conf/zmconfigd /staging/etc \
 && cd /core/opt/zextras/conf \
 && cp zmconfigd.cf *.in /staging/opt/zextras/share/configd/conf/ \
 && cp zmconfigd/*.cf /staging/opt/zextras/share/configd/conf/zmconfigd/ \
 && cp -r attrs /staging/opt/zextras/share/configd/conf/attrs \
 && printf 'zextras:x:999:999:Carbonio:/opt/zextras:/usr/sbin/nologin\n' > /staging/etc/passwd \
 && printf 'zextras:x:999:\n' > /staging/etc/group

FROM scratch

COPY --from=installer /staging/etc/passwd /etc/passwd
COPY --from=installer /staging/etc/group /etc/group
COPY --from=installer /staging/opt /opt
COPY --from=builder /out/configd /opt/zextras/bin/configd

ENV CARBONIO_BASE_DIR=/opt/zextras \
    CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd

USER 999

ENTRYPOINT ["/opt/zextras/bin/configd"]
