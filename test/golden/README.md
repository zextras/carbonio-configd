# Legacy parity corpus

Executable spec of the drop-in contract: the expected files in this directory
were produced by the **legacy Jython zmconfigd and the Java ProxyConfGen**, never
by the Go code. `cmd/configd/golden_test.go` renders every case with the
production Go wiring (`buildRuntime`, `runForcedMode`, plan/execute/restarts)
and diffs the result against the legacy output.

## Run

    make golden            # Go side only, offline, ~1s
    make golden-update     # regenerate expected/ from the legacy stack (podman, network)

`make golden-update` runs `scripts/golden/regen.sh`: a throwaway container of
`docker.io/carbonio/devel:202610290800-ce-single` (override with `GOLDEN_IMAGE`)
gets a real slapd loaded with the seed + case LDIFs, a localconfig.xml, and runs
the unmodified `libexec/zmconfigd` as `zextras` with `umask 0027`. `postconf`,
`ldap`/`zm*ctl` are replaced by a recorder that logs their argv
(`container/recorder`). After a legacy bump also run `scripts/golden/extract-tree.sh`.
Never edit `expected/` by hand or from Go output.

## Layout

    tree/                 legacy install tree the Go run reads (templates, zmconfigd.cf,
                          attrs.xml, directory seed, buildInfo); GPL-2.0-only, from the image
    base/<name>/          inputs shared by cases: ldif/*.ldif (applied in name order),
                          localconfig.xml (synthetic GOLDEN-* secrets)
    cases/<name>/case.txt description, base, mode (forced; daemon is supported by
                          scripts/golden/container/legacy_cycles.py but has no case yet)
    cases/<name>/cycle-N/ sections (forced run), ldif/ (deltas), localconfig.xml (replacement),
                          expected/{files/, modes.txt, exec.jsonl}

Cases: `forced-all` (single node, every section); `forced-features` (antispam,
cbpolicyd, DH params, MTA restrictions, a second mailstore, virtual-host domain
with its own certificate).

The Go side loads the same LDIF into an in-process LDAP server
(`internal/testutil/ldapfake` Directory), so the production LDAP client, attribute
inheritance and filters are exercised. The server is named by an IP literal
because both implementations resolve it (no DNS needed).

## Compared and normalized

Compared: every file the legacy run wrote under `conf/`, `common/conf/` and
`data/spamassassin/localrules/` (byte for byte, mode bits too), the final
`postconf` state (`-e k=v`, `-X k`), and the non-postconf commands in order.

Normalizations (all in `golden_test.go`/`golden.go`):
- the Go temp install root is mapped to `/opt/zextras`, the fake LDAP address to `127.0.0.1:389`;
- `postconf` is compared as the resulting key/value map: legacy runs one process
  per key from several threads, Go batches;
- Go's read-only `systemctl is-enabled` orchestration probe is ignored (legacy has none)
  and answers "not enabled" so restarts use the `zm*ctl` scripts like the legacy container.

## Findings fixed on this branch (found by the corpus)

| Area | Legacy | Go before |
|---|---|---|
| `web.upstream.target` | `https://zimbra_ssl` when `zimbraReverseProxySSLToUpstreamEnabled` (default TRUE) | static `http://zimbra` |
| `${vip}` listen prefix | empty with SNI (default), `<ip>:` / `[ip]:` without | raw `zimbraVirtualIPAddress`, no colon |
| domain explode | blank line after every domain block, last included | not after the last one |
| `listen.:addresses` | `listen<18 spaces>ip:port`; port of `web.https.port` | one space; port 0 when variable resolution order put it first |
| `web.ssl.dhparam.enabled` | checks `<workdir>/conf/dhparam.pem` | hard-coded `/opt/zextras/conf/dhparam.pem` |

## Known, not fixed

- Legacy `getAllMtaAuthURLs` with no `zimbraMtaAuthTarget` server renders the
  characters of "UNKNOWN OUTPUT" space-separated (`U N K N ...`); Go renders the
  string. Degenerate legacy bug, not matched.
- ProxyConfGen aborts on a virtual IP whose family contradicts `zimbraIPMode`; Go does not.
- Not covered yet: daemon-mode lifecycle cycles (restart decisions across cycles),
  `ldap` (cn=config) output, hosts with several proxies.
