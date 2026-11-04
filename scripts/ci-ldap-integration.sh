#!/bin/sh
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: AGPL-3.0-only

# Jenkins 'LDAP integration' stage: runs `make test-integration-ldap` inside a
# golang container started by the dockerd of the docker-build-v1 pod, from the
# 'dind' container of that pod (POSIX sh: it is alpine, no bash).
#
# The golang image has no docker CLI and the suite skips itself without a
# reachable docker, so the CLI binary and the socket of the daemon are mounted
# in. The test container shares the pod network (--network host) so the ports
# testcontainers maps are reachable on localhost. A skipped suite is a failure:
# the two OpenLDAP tests must report PASS.
set -eu

cd "$(dirname "$0")/.."

i=0
until docker info >/dev/null 2>&1; do
	i=$((i + 1))
	if [ "$i" -gt 60 ]; then
		echo "ci-ldap-integration: dockerd not ready after 60s" >&2
		exit 1
	fi
	sleep 1
done

docker run --rm --network host \
	-v "$PWD":/src -w /src \
	-v /var/run/docker.sock:/var/run/docker.sock \
	-v "$(command -v docker)":/usr/local/bin/docker:ro \
	-e TESTCONTAINERS_HOST_OVERRIDE=localhost \
	-e TESTCONTAINERS_RYUK_DISABLED=true \
	-e GOFLAGS=-buildvcs=false \
	docker.io/library/golang:1.27.3 \
	bash -o pipefail -c '
		make test-integration-ldap | tee /tmp/ldap-integration.log
		for t in TestOpenLDAP_ConfigdCycle TestOpenLDAP_StartTLS; do
			grep -q -- "--- PASS: $t " /tmp/ldap-integration.log || {
				echo "ci-ldap-integration: $t did not pass (skipped or missing)" >&2
				exit 1
			}
		done
	'
