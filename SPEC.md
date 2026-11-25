# ciano Specification

**Specification version:** 0.1
**Implementation version:** ciano v0.1.0
**Status:** implemented and tested through Phase 6; Phase 7 (kind) planned

This document is the normative description of `ciano`'s behavior. Where key words
such as MUST, MUST NOT, SHOULD, and MAY appear, they are used in the RFC 2119
sense. The [CNI specification](https://github.com/containernetworking/cni/blob/main/SPEC.md)
remains authoritative for the protocol itself; this document specifies how `ciano`
fulfils it.

## Contents

1. [Scope](#1-scope)
2. [Goals and non-goals](#2-goals-and-non-goals)
3. [Terminology](#3-terminology)
4. [Protocol contract](#4-protocol-contract)
5. [Configuration](#5-configuration)
6. [ADD](#6-add)
7. [DEL](#7-del)
8. [CHECK](#8-check)
9. [State model](#9-state-model)
10. [Failure and recovery](#10-failure-and-recovery)
11. [Privileges and security](#11-privileges-and-security)
12. [Compatibility](#12-compatibility)
13. [Testing](#13-testing)
14. [Design decisions and deviations](#14-design-decisions-and-deviations)
15. [Known limitations](#15-known-limitations)
16. [Roadmap](#16-roadmap)
17. [Repository layout](#17-repository-layout)
18. [References](#18-references)

## 1. Scope

`ciano` is a Linux-only CNI plugin of type `"ciano"`. It provides layer-3
connectivity to a container network namespace through a Linux bridge, delegating IP
allocation to an IPAM plugin (in practice `host-local`).

It is an educational project. The specification describes exactly what the code
does today, including deliberate omissions. It does not claim production
suitability.

### Implementation status

| Phase | Scope | Status | Commit |
|-------|-------|--------|--------|
| 0 | Toolchain, reference plugins, test sandbox | Complete | tooling only; `bin/` is gitignored |
| 1 | CNI skeleton (`skel`, ADD/DEL/CHECK stubs) | Complete | `7848425` |
| 2 | Configuration parsing | Complete | `7848425` |
| 3 | IPAM delegation to `host-local` | Complete | `28b53ae` |
| 4 | veth pair, bridge, addresses, routes | Complete | `089b45a` |
| 5 | Idempotent DEL | Complete | `3c6ce9c` |
| 6 | CHECK against `prevResult` | Complete | `4789ca1` |
| 7 | kind deployment | Planned | — |

## 2. Goals and non-goals

### Goals

- Implement `ADD`, `DEL`, and `CHECK` correctly according to the CNI protocol.
- Produce the same externally visible network shape as the reference `bridge`
  plugin for the supported configuration subset.
- Be robust under retries: no leaked veths or allocations on failed `ADD`;
  idempotent `DEL` that tolerates missing namespaces and links.
- Remain readable and small enough to serve as a learning artifact.
- Be testable without root or Docker through unprivileged user namespaces.

### Non-goals

- Production hardening, performance tuning, or long-term maintenance guarantees.
- Advanced bridge features: VLANs, VLAN trunks, promiscuous mode, port isolation,
  MAC spoof checking, or disabling the container interface.
- IPAM implementations other than delegation (no built-in allocator).
- IPv6 certification (the code is family-aware, but only IPv4 is tested).
- `GC` or `STATUS` commands.
- Multi-node cluster networking. Multi-node support requires per-node IPAM subnets
  and cross-node routes; see [section 15](#15-known-limitations).

## 3. Terminology

| Term | Meaning |
|------|---------|
| **runtime** | The process invoking the plugin (containerd, CRI-O, kubelet via CRI, or `cnitool`). |
| **plugin** | The `ciano` executable invoked for one CNI command. |
| **IPAM plugin** | The delegate that allocates addresses, here `host-local`. |
| **network namespace (netns)** | The isolated network stack of a pod sandbox. |
| **sandbox** | The pause container whose netns is shared by the pod's containers. |
| **veth pair** | Two virtual Ethernet interfaces joined back-to-back; packets entering one end emerge from the other. |
| **bridge** | A Linux virtual switch, named `cni0` by default, to which host veth ends are attached. |
| **prevResult** | The result JSON returned by a prior `ADD`, supplied back by the runtime on `CHECK`. |
| **host netns** | The network namespace in which the plugin normally runs and in which the bridge lives. |

## 4. Protocol contract

### 4.1 Execution model

The runtime MUST execute the plugin binary as a child process. The plugin MUST read
its network configuration from stdin and MUST write exactly one result or error
object to stdout. `ciano` is stateless across invocations: each command starts a
new process with no in-memory history.

The runtime locates the plugin by mapping the configuration field `"type"` to an
executable name and searching the directories in `CNI_PATH`. It locates the
configuration by `"name"` under `NETCONFPATH` (default `/etc/cni/net.d`).

### 4.2 Environment variables

| Variable | Requirement | Use in `ciano` |
|----------|-------------|----------------|
| `CNI_COMMAND` | MUST | Selects `ADD`, `DEL`, or `CHECK`. |
| `CNI_CONTAINERID` | MUST | Identity of the sandbox. Used for host-local bookkeeping and the deterministic host veth name. |
| `CNI_NETNS` | MUST for `ADD`/`CHECK`; MAY be empty for `DEL` | Path to the sandbox netns. |
| `CNI_IFNAME` | MUST | Interface name inside the container, normally `eth0`. |
| `CNI_ARGS` | MAY | Parsed by `skel`; not otherwise used. |
| `CNI_PATH` | MUST | Colon-separated directories used to find delegated plugin binaries. |

### 4.3 Streams and exit status

| Stream | Content |
|--------|---------|
| stdin | The network configuration JSON. |
| stdout | `ADD`: the result JSON. `DEL`: nothing on success. `CHECK`: nothing on success. On failure: an error JSON object. |
| stderr | Human-readable logs only. The runtime MUST NOT interpret it. |
| exit | `0` on success; non-zero on failure. |

The plugin MUST NOT write logs to stdout, because the runtime parses stdout as JSON.
All diagnostics from `ciano` are written to stderr.

Error objects are produced by `skel`. Generic errors returned by `cmdAdd`,
`cmdDel`, or `cmdCheck` become:

```json
{"code": 999, "msg": "<error text>"}
```

Protocol-level failures use CNI error codes (for example, code `6` for content that
could not be decoded). No `cniVersion` field is included in the error object
emitted by `skel` v1.3.1.

### 4.4 Version negotiation

`ciano` advertises support for all CNI configuration versions in the library's
`version.All`:

```text
0.1.0  0.2.0  0.3.0  0.3.1  0.4.0  1.0.0  1.1.0
```

The result is printed in the version requested by the configuration
(`types.PrintResult(result, conf.CNIVersion)`). Internally the result is first
converted to the library's implemented type (`current.ImplementedSpecVersion`,
`1.1.0` in cni v1.3.1), then converted down if necessary.

The sample configuration uses `1.0.0`, which is the version used by all tests.

### 4.5 Commands

`ciano` registers exactly three callbacks:

| Command | Behavior |
|---------|----------|
| `ADD` | Allocates an address, creates and wires the endpoint, returns the result. |
| `DEL` | Removes the endpoint and releases the allocation. Idempotent. |
| `CHECK` | Verifies live state against `prevResult`. Read-only. |

`GC` and `STATUS` are not implemented. Invoking them is a protocol error reported
by `skel`.

### 4.6 `prevResult`

For `CHECK`, the runtime MUST include in the configuration the result previously
returned by `ADD`, under the key `"prevResult"`. The plugin parses it as follows:

1. `skel` unmarshals the whole config into `NetConf`, which embeds
   `types.NetConf`. The raw object lands in `RawPrevResult`.
2. `version.ParsePrevResult(&conf.NetConf)` decodes `RawPrevResult` into
   `conf.PrevResult` using the result's own `cniVersion`.
3. `current.NewResultFromResult(conf.PrevResult)` converts it into the concrete
   spec-1.x result type used for comparisons.

If `prevResult` is missing, `CHECK` MUST fail.

## 5. Configuration

### 5.1 Schema

`ciano` embeds the standard CNI fields (`cniVersion`, `name`, `type`, `args`,
`dns`, `runtimeConfig`, `capabilities`, `prevResult`) and adds:

| JSON field | Go type | Default | Required | Status |
|------------|---------|---------|----------|--------|
| `cniVersion` | string | — | yes | Used for version negotiation and result printing. |
| `name` | string | — | yes | Network identity; becomes host-local's state directory name. |
| `type` | string | — | yes | Must be `"ciano"` for the runtime to select this binary. |
| `bridge` | string | `cni0` | no | Bridge name. |
| `isGateway` | bool | `false` | no | Implemented. |
| `isDefaultGateway` | bool | `false` | no | Implemented; implies `isGateway`. |
| `ipMasq` | bool | `false` | no | **Parsed but not implemented**; `ADD` prints a warning and creates no NAT rules. |
| `mtu` | int | `0` | no | `0` leaves the kernel default; applied at veth creation and when creating the bridge. |
| `hairpinMode` | bool | `false` | no | Implemented; set on the host veth. |
| `ipam.type` | string | — | yes | Delegate plugin name, e.g. `host-local`. |
| `ipam.subnet` | string | — | host-local-specific | Passed through. |
| `ipam.ranges` | array | — | host-local-specific | Passed through. |
| `ipam.routes` | array | — | host-local-specific | Returned by host-local and installed in the container. |
| `ipam.dataDir` | string | `/var/lib/cni/networks` | host-local-specific | Host-local state location. Tests override this. |

Fields inherited from `types.NetConf` but not used by `ciano`:

- `args`, `runtimeConfig`, `capabilities`: parsed but ignored.
- `dns`: parsed but not applied. DNS information returned by the IPAM plugin is
  propagated in the result.
- `prevResult`: used only by `CHECK`.

Unknown JSON fields are ignored (the decoder is not strict).

### 5.2 Defaults and validation

- `bridge` defaults to `cni0` before unmarshalling, so an absent key preserves the
  default; an explicit `"bridge": ""` overrides it with an empty string and will
  fail later.
- `isDefaultGateway: true` sets `isGateway` to `true` (section 6.3).
- A JSON syntax error fails the command with a decode error.
- `ADD` fails if the IPAM plugin returns zero IP addresses.
- `CHECK` fails if `prevResult` is missing, empty, or contains no IP addresses.

### 5.3 Example

```json
{
  "cniVersion": "1.0.0",
  "name": "ciano",
  "type": "ciano",
  "bridge": "cni0",
  "isGateway": true,
  "isDefaultGateway": true,
  "ipam": {
    "type": "host-local",
    "subnet": "10.22.0.0/16",
    "routes": [{ "dst": "0.0.0.0/0" }]
  }
}
```

## 6. ADD

### 6.1 Preconditions

- `CNI_COMMAND=ADD`.
- `CNI_CONTAINERID`, `CNI_NETNS`, and `CNI_IFNAME` are non-empty.
- The configuration parses and contains `ipam.type`.
- The process has `CAP_NET_ADMIN` and `CAP_SYS_ADMIN` (typically root).

### 6.2 Algorithm

1. Load and validate the configuration. If `isDefaultGateway` is true, set
   `isGateway` to true.
2. Delegate IP allocation:
   `ipam.ExecAdd(conf.IPAM.Type, args.StdinData)`.
   - The delegate is found in `CNI_PATH`.
   - The delegate inherits the environment; `CNI_COMMAND` is overridden to `ADD`.
   - The **entire** network configuration is passed on stdin.
3. Convert the returned `types.Result` with
   `current.NewResultFromResult` and require at least one IP.
4. Register a failure-cleanup function (section 6.6).
5. Open `CNI_NETNS` with `ns.GetNS` and keep it open for the duration.
6. Prepare the result (section 6.3):
   - Derive missing gateways and collect bridge gateway addresses.
   - Add a default route if configured to do so.
   - Set each IP's `interface` index to `2`.
7. Ensure the bridge exists, is a bridge, and is up
   (`ensureBridge`). A missing bridge is created with the configured MTU.
8. Delete any pre-existing host veth with the deterministic name derived for this
   container (section 6.4). This makes retries safe after a crashed attempt.
9. Create the veth pair **from inside the container netns** using
   `ip.SetupVethWithName`:
   - container end: `CNI_IFNAME`;
   - host end: the deterministic name, created directly in the host netns
     (`PeerNamespace`);
   - MTU applied to the pair.
10. Build the result's interface list:
    0. the bridge (name, MAC),
    1. the host veth (name, MAC),
    2. the container interface (name, MAC, `sandbox` = netns path).
11. Configure the container interface with `ipam.ConfigureIface` inside the netns:
    add every IP address, set the link up, install every route. For routes without
    an explicit gateway, the first gateway of the same address family is used.
12. Attach the host veth to the bridge with `LinkSetMaster`, set hairpin mode
    (`true` or `false`), and set the link up.
13. For each collected gateway address:
    - add `<gateway>/<container-mask>` to the bridge unless the IP is already
      present;
    - enable IPv4 or IPv6 forwarding.
14. Re-read the bridge's MAC address (it can change while ports are added) and
    update the result.
15. Mark the operation successful and print the result in `conf.CNIVersion`.

### 6.3 Gateway and default-route rules

The plugin distinguishes two related flags:

- `isGateway`: the bridge owns the gateway address(es) and the host forwards
  traffic.
- `isDefaultGateway`: additionally guarantees a default route.

Rules:

- **G1.** If `isGateway` is true and an IP has no gateway, the gateway is the first
  usable address of that IP's subnet (`network + 1`).
- **G2.** Every gateway is assigned to the bridge using the *address* mask.
- **G3.** If `isDefaultGateway` is true and the IPAM result has no default route
  for that address family, a `0.0.0.0/0` or `::/0` route via the IP's gateway is
  appended. An existing default route is never duplicated.
- **G4.** Inside the container, a route with an explicit `gw` uses it; a route
  without one uses the first gateway of the same family (this is the behavior of
  `ipam.ConfigureIface`).
- **G5.** Forwarding is enabled only for families that have a gateway.

These rules reproduce the observable behavior of the reference `bridge` plugin:
containers ARP for the gateway, the bridge answers, and the host routes between the
bridge and the outside.

### 6.4 Host veth naming

The host interface name MUST be deterministic so that `DEL` and `CHECK` can find it
without runtime state:

```text
hostVethName = "veth" + hex( sha256( containerID + "/" + ifName )[0:5] )
```

Properties:

- 14 characters (`veth` + 10 hex digits), within the Linux 15-character limit
  (`IFNAMSIZ` includes the NUL).
- Stable for a given container ID and interface name.
- A stale link with the same name is deleted before a new pair is created.
- Two concurrent `ADD`s for the same container ID/interface name resolve to the
  same name and are not supported concurrently.

### 6.5 Result

The result contains the three interfaces and the IPAM-provided addresses/routes.
Example:

```json
{
  "cniVersion": "1.0.0",
  "interfaces": [
    { "name": "cni0", "mac": "4a:51:16:36:de:0c" },
    { "name": "veth1f8cf934ed", "mac": "4a:51:16:36:de:0c" },
    {
      "name": "eth0",
      "mac": "be:ac:91:35:3a:6c",
      "sandbox": "/var/run/netns/example"
    }
  ],
  "ips": [
    {
      "address": "10.22.0.3/16",
      "gateway": "10.22.0.1",
      "interface": 2
    }
  ],
  "routes": [{ "dst": "0.0.0.0/0" }]
}
```

Notes:

- `interface: 2` points into `interfaces` (the container interface).
- The `routes` objects come from IPAM; a route usually has no `gw` field, and the
  gateway fallback described in G4 is applied when the route is installed.
- `dns` is copied from the IPAM result when present.
- The bridge MAC is refreshed after all host-side changes.

### 6.6 Failure handling

If any step after a successful IPAM allocation fails, the deferred cleanup MUST:

1. Delete the host veth with the deterministic name, ignoring "not found".
2. Release the allocation with `ipam.ExecDel`.

Cleanup errors are logged to stderr and do not replace the original error. The
function that declares success is called only after the network is fully
configured, immediately before printing the result.

If `ADD` fails before IPAM succeeds, no allocation exists and no cleanup is
required. If the IPAM result cannot be converted or contains no IPs, the allocation
is released explicitly.

## 7. DEL

`DEL` MUST be idempotent: repeated calls, a missing netns, and an already-missing
veth MUST all succeed.

### 7.1 Algorithm

1. Load the configuration.
2. Compute the host veth name from `CNI_CONTAINERID` and `CNI_IFNAME`.
3. Tear down the endpoint (section 7.2).
4. If `ipam.type` is non-empty, call `ipam.ExecDel(conf.IPAM.Type, stdin)`.
5. Return success.

`DEL` MUST NOT delete the bridge or its gateway address: they are shared by all
containers on the node.

### 7.2 Endpoint teardown decision tree

```text
look up host veth by deterministic name
├─ exists ────────────────► delete it ──► done (the pair disappears)
├─ lookup error ≠ not found ─► return the error
└─ not found
   ├─ CNI_NETNS empty ────► done
   └─ enter netns (WithNetNSPath)
      ├─ netns path missing ─► done (netns and pair died together)
      └─ delete CNI_IFNAME inside
         ├─ deleted ───────► done
         ├─ link not found ─► done
         └─ other error ────► return the error
```

Deleting either end of a veth pair destroys both ends, so the host-side lookup is
sufficient in the normal case and works even when the netns is already gone. The
netns fallback covers endpoints not created under the deterministic name (for
example, after a manual rename).

### 7.3 Ordering rationale

The endpoint MUST be removed before the allocation is released:

- If the allocation were freed first and veth deletion then failed, another pod
  could receive the same IP while the stale endpoint still had it configured,
  creating an address conflict.
- Keeping the allocation until the endpoint is gone makes a failed `DEL` retry
  safe.

If `ExecDel` fails after the endpoint is removed, the next `DEL` retry still
releases the allocation; no netns is needed for that step.

## 8. CHECK

### 8.1 Preconditions

- `CNI_COMMAND=CHECK`.
- The configuration contains `prevResult` (CNI spec 0.4.0 and later).
- `CNI_NETNS` is a valid path to the sandbox netns.

`CHECK` is read-only: it MUST NOT repair or modify state.

### 8.2 Algorithm

1. Require `prevResult`; parse and convert it to the current result type. Require
   at least one IP.
2. If `ipam.type` is non-empty, call `ipam.ExecCheck`. For `host-local`, this
   verifies that an allocation file for this container ID and interface still
   exists.
3. Open the netns.
4. Perform host-side checks (outside the netns):
   - the bridge exists, is a bridge, and is up;
   - the host veth exists, is a veth, is up, and its master is the bridge;
   - if `prevResult` names the host veth and records a MAC, the MAC matches;
   - if `prevResult` records a container sandbox, it equals `CNI_NETNS`;
   - for each gateway in `prevResult`, the bridge owns that address when
     `isGateway` is true.
5. Enter the netns and perform container-side checks:
   - `CNI_IFNAME` exists, is a veth, and is up;
   - its MAC matches `prevResult` when recorded;
   - every address in `prevResult` is present
     (`ip.ValidateExpectedInterfaceIPs`, which also checks the connected route);
   - every route in `prevResult` exists (`ip.ValidateExpectedRoute`; default
     routes are matched without a destination filter).

Any mismatch returns an error.

### 8.3 Deliberate omissions

`CHECK` does **not** compare the bridge MAC with `prevResult`. A Linux bridge
derives its MAC from its ports; adding a container with a lower-MAC port can
change the bridge MAC even though nothing is wrong. Our integration tests observed
this when a second container joined. Treating a volatile attribute as a failure
would create false negatives, so the bridge's MAC is not an invariant.

`CHECK` also does not attempt to detect or fix:
- wrong MTU,
- extra addresses or routes,
- hairpin mode,
- forwarding sysctls.

Only the conditions listed in section 8.2 are enforced.

## 9. State model

`ciano` keeps no persistent state of its own. Correct behavior across invocations
depends on four stores owned by others:

| State | Location | Owner | Lifetime |
|-------|----------|-------|----------|
| Sandbox netns | kernel, bind-mounted path | runtime | sandbox lifetime |
| veth pair | kernel (both netns) | created by plugin; kernel-owned | until either end is deleted, or the container netns dies |
| Bridge, gateway address, forwarding flags | host netns | shared infrastructure | until manually removed; `DEL` does not remove them |
| Container addresses and routes | container netns | plugin-created | until netns destruction or explicit deletion |
| IP allocations | host-local `dataDir` | `host-local` | until `ExecDel` (or manual/GC cleanup) |
| `prevResult` | runtime's sandbox metadata | runtime | sandbox lifetime |
| Plugin memory | none | — | process lifetime only |

### Host-local allocation files

With `dataDir` at its default (`/var/lib/cni/networks`), `host-local` stores state
under `<dataDir>/<network-name>/`:

- `<IP>`: a text file containing the container ID and interface name
  (`<containerID>\n<ifname>`). Its existence is the allocation.
  Reservation uses an atomic exclusive create (`O_CREATE|O_EXCL`), so concurrent
  allocators cannot both claim the same IP.
- `last_reserved_ip.<rangeID>`: round-robin cursor. The next allocation starts
  after this address, which is why repeated add/delete cycles rotate through the
  range.
- `lock`: serializes allocation operations for the network.

`host-local`'s `CHECK` searches these files by container ID/interface name.
`host-local`'s `DEL` removes the matching files.

## 10. Failure and recovery

- **Failed `ADD`:** the deferred cleanup removes the host veth and releases the
  IPAM allocation. If cleanup itself fails, the error is logged; a later `DEL` is
  the recovery path.
- **Failed or interrupted `DEL`:** the allocation remains (if `ExecDel` did not
  run), and the veth may remain. Retrying `DEL` finishes the job. `DEL` tolerates
  every partially torn-down state.
- **Retry after a leaked allocation:** `host-local` refuses a second allocation
  for the same container ID/interface with
  `"<ip> has been allocated to <id>, duplicate allocation is not allowed"`.
  The correct recovery is to run `DEL` first (or remove the allocation file when
  the container is known to be gone).
- **Stale veth:** `ADD` pre-deletes a link with its own deterministic name, so a
  leftover from a crashed attempt cannot cause `EEXIST`.

There is no automatic garbage collection in `ciano`; `GC` is not implemented.

## 11. Privileges and security

Real operations require:

- `CAP_NET_ADMIN`: create/delete links, addresses, routes; configure the bridge;
  write forwarding sysctls.
- `CAP_SYS_ADMIN`: open and `setns()` into the sandbox netns.

In practice this means the plugin runs as root. Tests can avoid host privileges by
running inside:

- a privileged container, or
- an unprivileged user namespace (`unshare -Urn`) where the process has the
  required capabilities over namespaces it owns.

`ciano` handles no secrets. The deterministic host veth name contains ten hex
digits of a SHA-256 hash of the container ID and interface name; this is not a
security boundary.

## 12. Compatibility

| Component | Version |
|-----------|---------|
| Go | 1.27.0 |
| `github.com/containernetworking/cni` | v1.3.1 |
| `github.com/containernetworking/plugins` | v1.9.1 |
| `github.com/vishvananda/netlink` | v1.3.1 |
| Operating system | Linux only (netlink, network namespaces) |
| CNI config versions | 0.1.0–1.1.0 advertised; 1.0.0 tested |
| `CHECK` | Meaningful for config version 0.4.0+ |

## 13. Testing

### Unit tests

Run with `make test` (no privileges required). Coverage:

- configuration parsing, defaults, and invalid JSON;
- deterministic veth naming (format, length, stability, uniqueness);
- gateway derivation from a subnet;
- result preparation: gateway insertion, default-route de-duplication, interface
  index assignment.

### Integration harness

`hack/test-userns.sh` (also `make test-userns`) runs as a normal user. It
re-executes itself under `unshare -Urn`, redirects host-local's `dataDir` to a
temporary directory, and uses `unshare -n sleep` processes as container
namespaces. Scenarios:

| Scenario | Coverage |
|----------|----------|
| `add-del` | two containers, gateway ping, pod-to-pod ping, ARP resolution, full cleanup |
| `del-edge` | double DEL, netns destroyed before DEL, host veth deleted manually, empty `CNI_NETNS`, DEL for a never-added container |
| `check` | positive CHECK; missing `prevResult`; interface down; address removed; host veth detached; wrong netns; two live containers |

### Privileged / runtime testing

- `cnitool` in a root shell or privileged container (its result cache needs
  `/var/lib/cni`).
- kind (Phase 7, planned): see the README.

## 14. Design decisions and deviations

| Decision | Reference `bridge` behavior | `ciano` behavior and rationale |
|----------|-----------------------------|--------------------------------|
| Host veth name | Random (`veth` + random bytes), recorded only in the runtime's result | Deterministic hash of container ID + interface. Gives `DEL`/`CHECK` a host-side handle independent of the netns and runtime state; enables stale-link pre-deletion. Cost: name collisions are possible in principle (40-bit space) and concurrent ADDs for the same identity are unsupported. |
| `DEL` lookup order | Enter the netns and delete the container end | Host veth first (works with a dead/unlinked netns), then netns fallback. Covers edge cases the reference leaks: unlinked netns paths and renamed container interfaces. |
| Bridge MAC in `CHECK` | Compared against `prevResult` | Deliberately not compared. The bridge MAC can change when ports join/leave, so it is not a functional invariant. |
| State | Stateless; relies on netns + runtime result | Same. |
| Route installation | Gateway fallback inside `ipam.ConfigureIface` | Same library call, so behavior matches. |
| Default gateway | `calcGateways` may add a default route | Same rule, simplified to per-family handling. |
| `ipMasq` | Implemented via iptables/nftables | Not implemented; parsed and warned about. |
| Advanced options | VLANs, trunks, promisc, port isolation, MAC spoof check, disable interface | Not implemented. |
| GC/STATUS | STATUS implemented; GC not | Neither implemented. |
| Existing bridge MTU | Not reconciled | Not reconciled (documented limitation). |

## 15. Known limitations

1. **`ipMasq` is not implemented.** Pods have no NAT for traffic leaving the node
   unless something else provides it. A warning is printed when the option is set.
2. **Kubernetes/kind is not verified.** The intended setup is single-node. In kind,
   the plugin and config must be mounted into the node via `extraMounts`.
3. **Multi-node networking is out of scope.** Each node would need a distinct
   host-local subnet and routes for the other nodes' pod subnets.
4. **IPv6 is untested.** The code is family-aware, but only IPv4 was exercised.
5. **No GC or STATUS.**
6. **Bridge MTU is not reconciled.** `mtu` applies when a bridge is created; an
   existing bridge keeps its MTU.
7. **Shared state persists.** The bridge, gateway address, host-local cursor, and
   lock files remain after the last container is deleted.
8. **Not production-hardened.** No CI, fuzzing, upgrade path, rate limiting, or
   hardening review.
9. **Same-identity concurrency is unsupported.** Two simultaneous ADDs for the same
   `CNI_CONTAINERID`/`CNI_IFNAME` would target the same deterministic veth name and
   the same allocation identity.

## 16. Roadmap

1. Phase 7: single-node kind deployment and pod-to-pod verification, including
   `extraMounts` and `disableDefaultCNI`.
2. Implement `ipMasq` (setup and teardown) with an nftables/iptables backend.
3. Optional multi-node kind experiment: per-node host-local subnets plus
   cross-node routes.
4. `GC` and `STATUS`.
5. IPv6 testing and hardening.
6. CI (unit + rootless integration) and documentation polish.

## 17. Repository layout

```text
main.go             CLI wiring (skel callbacks) and command orchestration
bridge.go           netlink operations, result preparation, state validation
netconf.go          NetConf struct and loadNetConf
bridge_test.go      unit tests for network/result helpers
netconf_test.go     unit tests for configuration parsing
conf/10-ciano.conf  sample network configuration
hack/test-userns.sh rootless integration harness
kind/               kind cluster configuration (Phase 7, planned)
Makefile            build/test/vet/fmt/test-userns targets
bin/                built binaries and downloaded CNI plugins (gitignored)
SPEC.md             this document
README.md           user-facing overview and quick start
```

## 18. References

- CNI specification: <https://github.com/containernetworking/cni/blob/main/SPEC.md>
- CNI plugins, reference `bridge` implementation:
  <https://github.com/containernetworking/plugins/blob/v1.9.1/plugins/main/bridge/bridge.go>
- CNI plugins, `host-local`:
  <https://github.com/containernetworking/plugins/tree/v1.9.1/plugins/ipam/host-local>
- `vishvananda/netlink`: <https://github.com/vishvananda/netlink>
- Linux network namespaces and veth: `ip-netns(8)`, `ip-link(8)`
