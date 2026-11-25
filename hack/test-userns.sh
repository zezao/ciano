#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SCRIPT="$ROOT/hack/test-userns.sh"
BIN="$ROOT/bin"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

if [ "${CIANO_USERNS:-0}" != "1" ]; then
  command -v unshare >/dev/null 2>&1 || fail "unshare not found (install util-linux)"
  command -v ip >/dev/null 2>&1 || fail "ip not found (install iproute2)"
  command -v nsenter >/dev/null 2>&1 || fail "nsenter not found (install util-linux)"
  command -v ping >/dev/null 2>&1 || fail "ping not found (install iputils-ping)"
  [ -x "$BIN/host-local" ] || fail "missing $BIN/host-local; download cni-plugins v1.9.1 into bin/ (see README)"
  if [ ! -x "$BIN/ciano" ]; then
    echo "building $BIN/ciano"
    (cd "$ROOT" && go build -o "$BIN/ciano" .)
  fi

  set +e
  CIANO_USERNS=1 unshare -Urn bash "$SCRIPT" "$@"
  status=$?
  set -e
  exit "$status"
fi

SCENARIO=${1:-all}
case "$SCENARIO" in
  all|add-del|del-edge|check) ;;
  *) fail "usage: $0 [all|add-del|del-edge|check]" ;;
esac

[ -x "$BIN/ciano" ] || fail "missing $BIN/ciano"
[ -x "$BIN/host-local" ] || fail "missing $BIN/host-local"

WORK=$(mktemp -d "${TMPDIR:-/tmp}/ciano-userns.XXXXXX")
PIDS=()
NS_PID=
cleanup() {
  if [ "${#PIDS[@]}" -gt 0 ]; then
    kill "${PIDS[@]}" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/networks"
cat > "$WORK/conf.json" <<EOF
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
    "dataDir": "$WORK/networks",
    "routes": [{"dst": "0.0.0.0/0"}]
  }
}
EOF

run_ciano() {
  local cmd=$1 id=$2 ns=$3 cfg=$4
  env CNI_COMMAND="$cmd" \
      CNI_CONTAINERID="$id" \
      CNI_NETNS="$ns" \
      CNI_IFNAME=eth0 \
      CNI_PATH="$BIN" \
      "$BIN/ciano" < "$cfg"
}

new_ns() {
  unshare -n sleep 600 &
  NS_PID=$!
  PIDS+=("$NS_PID")
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if [ -e "/proc/$NS_PID/ns/net" ]; then
      return 0
    fi
    sleep 0.1
  done
  fail "network namespace holder did not start"
}

alloc_count() {
  find "$WORK/networks/ciano" -maxdepth 1 -type f -name '10.22.*' 2>/dev/null | wc -l | tr -d ' '
}

host_veths() {
  ip -o link show type veth 2>/dev/null | sed -n 's/^[0-9]*: \([^:@]*\)@.*/\1/p'
}

ip_from() {
  sed -n 's/.*"address": "\([0-9.]*\)\/.*/\1/p' "$1" | head -n1
}

gateway_from() {
  sed -n 's/.*"gateway": "\([0-9.]*\)".*/\1/p' "$1" | head -n1
}

make_check_config() {
  local result=$1 out=$2
  {
    echo "{"
    echo '  "cniVersion": "1.0.0",'
    echo '  "name": "ciano",'
    echo '  "type": "ciano",'
    echo '  "bridge": "cni0",'
    echo '  "isGateway": true,'
    echo '  "isDefaultGateway": true,'
    printf '  "ipam": {"type":"host-local","subnet":"10.22.0.0/16","dataDir":"%s/networks","routes":[{"dst":"0.0.0.0/0"}]},\n' "$WORK"
    printf '  "prevResult": '
    cat "$result"
    echo
    echo "}"
  } > "$out"
}

expect_fail() {
  local desc=$1 pattern=$2
  shift 2
  if "$@" >"$WORK/fail.log" 2>&1; then
    fail "$desc unexpectedly succeeded"
  fi
  if ! grep -q "$pattern" "$WORK/fail.log"; then
    cat "$WORK/fail.log" >&2
    fail "$desc did not report '$pattern'"
  fi
}

scenario_add_del() {
  new_ns
  local pa=$NS_PID
  new_ns
  local pb=$NS_PID
  local nsa="/proc/$pa/ns/net"
  local nsb="/proc/$pb/ns/net"

  run_ciano ADD harness-a "$nsa" "$WORK/conf.json" >"$WORK/a.json" 2>/dev/null
  run_ciano ADD harness-b "$nsb" "$WORK/conf.json" >"$WORK/b.json" 2>/dev/null

  local aip bip
  aip=$(ip_from "$WORK/a.json")
  bip=$(ip_from "$WORK/b.json")
  [ -n "$aip" ] || fail "could not parse address from ADD result"
  [ -n "$bip" ] || fail "could not parse address from ADD result"

  nsenter -t "$pa" -n ping -c1 -W3 10.22.0.1 >/dev/null || fail "gateway unreachable"
  nsenter -t "$pa" -n ping -c1 -W3 "$bip" >/dev/null || fail "pod-to-pod ping failed"

  local veths
  veths=$(host_veths | wc -l | tr -d ' ')
  [ "$veths" -eq 2 ] || fail "expected 2 host veths, found $veths"

  run_ciano DEL harness-a "$nsa" "$WORK/conf.json" 2>/dev/null
  run_ciano DEL harness-b "$nsb" "$WORK/conf.json" 2>/dev/null

  [ -z "$(host_veths)" ] || fail "veths remain after DEL"
  [ "$(alloc_count)" -eq 0 ] || fail "allocations remain after DEL"

  kill "$pa" "$pb" 2>/dev/null || true
  echo "PASS add-del"
}

scenario_del_edge() {
  new_ns
  local pa=$NS_PID
  local nsa="/proc/$pa/ns/net"

  run_ciano ADD edge-a "$nsa" "$WORK/conf.json" >/dev/null 2>&1
  run_ciano DEL edge-a "$nsa" "$WORK/conf.json" >/dev/null 2>&1
  run_ciano DEL edge-a "$nsa" "$WORK/conf.json" >/dev/null 2>&1
  [ -z "$(host_veths)" ] || fail "double DEL left a veth behind"
  kill "$pa" 2>/dev/null || true

  new_ns
  local pb=$NS_PID
  local nsb="/proc/$pb/ns/net"
  run_ciano ADD edge-b "$nsb" "$WORK/conf.json" >/dev/null 2>&1
  kill "$pb" 2>/dev/null || true
  wait "$pb" 2>/dev/null || true
  sleep 0.3
  [ -z "$(host_veths)" ] || fail "veth survived netns destruction"
  run_ciano DEL edge-b "$nsb" "$WORK/conf.json" >/dev/null 2>&1

  new_ns
  local pc=$NS_PID
  local nsc="/proc/$pc/ns/net"
  run_ciano ADD edge-c "$nsc" "$WORK/conf.json" >/dev/null 2>&1
  local veth
  veth=$(host_veths)
  [ -n "$veth" ] || fail "host veth not found"
  ip link del "$veth"
  run_ciano DEL edge-c "$nsc" "$WORK/conf.json" >/dev/null 2>&1

  new_ns
  local pd=$NS_PID
  local nsd="/proc/$pd/ns/net"
  run_ciano ADD edge-d "$nsd" "$WORK/conf.json" >/dev/null 2>&1
  run_ciano DEL edge-d "" "$WORK/conf.json" >/dev/null 2>&1

  new_ns
  local pe=$NS_PID
  local nse="/proc/$pe/ns/net"
  run_ciano DEL never-added "$nse" "$WORK/conf.json" >/dev/null 2>&1

  [ -z "$(host_veths)" ] || fail "veths remain after DEL edge cases"
  [ "$(alloc_count)" -eq 0 ] || fail "allocations remain after DEL edge cases"

  kill "$pc" "$pd" "$pe" 2>/dev/null || true
  echo "PASS del-edge"
}

scenario_check() {
  new_ns
  local pa=$NS_PID
  local nsa="/proc/$pa/ns/net"
  run_ciano ADD check-a "$nsa" "$WORK/conf.json" >"$WORK/a.json" 2>/dev/null
  make_check_config "$WORK/a.json" "$WORK/a-check.json"

  run_ciano CHECK check-a "$nsa" "$WORK/a-check.json" >/dev/null 2>&1 || fail "positive CHECK failed"
  expect_fail "CHECK without prevResult" "requires a prevResult" \
    run_ciano CHECK check-a "$nsa" "$WORK/conf.json"

  nsenter -t "$pa" -n ip link set eth0 down
  expect_fail "CHECK with interface down" "is down" \
    run_ciano CHECK check-a "$nsa" "$WORK/a-check.json"
  nsenter -t "$pa" -n ip link set eth0 up
  local gw
  gw=$(gateway_from "$WORK/a.json")
  nsenter -t "$pa" -n ip route add default via "$gw" dev eth0
  run_ciano CHECK check-a "$nsa" "$WORK/a-check.json" >/dev/null 2>&1 || fail "CHECK did not recover"
  run_ciano DEL check-a "$nsa" "$WORK/conf.json" >/dev/null 2>&1
  kill "$pa" 2>/dev/null || true

  new_ns
  local pb=$NS_PID
  local nsb="/proc/$pb/ns/net"
  run_ciano ADD check-b "$nsb" "$WORK/conf.json" >"$WORK/b.json" 2>/dev/null
  make_check_config "$WORK/b.json" "$WORK/b-check.json"
  run_ciano CHECK check-b "$nsb" "$WORK/b-check.json" >/dev/null 2>&1 || fail "positive CHECK failed"
  local bip
  bip=$(ip_from "$WORK/b.json")
  nsenter -t "$pb" -n ip addr del "$bip/16" dev eth0
  expect_fail "CHECK with address removed" "address validation failed" \
    run_ciano CHECK check-b "$nsb" "$WORK/b-check.json"
  run_ciano DEL check-b "$nsb" "$WORK/conf.json" >/dev/null 2>&1
  kill "$pb" 2>/dev/null || true

  new_ns
  local pc=$NS_PID
  local nsc="/proc/$pc/ns/net"
  run_ciano ADD check-c "$nsc" "$WORK/conf.json" >"$WORK/c.json" 2>/dev/null
  make_check_config "$WORK/c.json" "$WORK/c-check.json"
  run_ciano CHECK check-c "$nsc" "$WORK/c-check.json" >/dev/null 2>&1 || fail "positive CHECK failed"
  local cveth
  cveth=$(host_veths)
  [ -n "$cveth" ] || fail "host veth not found"
  ip link set "$cveth" nomaster
  expect_fail "CHECK with detached host veth" "not attached to bridge" \
    run_ciano CHECK check-c "$nsc" "$WORK/c-check.json"
  run_ciano DEL check-c "$nsc" "$WORK/conf.json" >/dev/null 2>&1
  kill "$pc" 2>/dev/null || true

  new_ns
  local pd=$NS_PID
  new_ns
  local pe=$NS_PID
  local nsd="/proc/$pd/ns/net"
  local nse="/proc/$pe/ns/net"
  run_ciano ADD check-d "$nsd" "$WORK/conf.json" >"$WORK/d.json" 2>/dev/null
  run_ciano ADD check-e "$nse" "$WORK/conf.json" >"$WORK/e.json" 2>/dev/null
  make_check_config "$WORK/d.json" "$WORK/d-check.json"
  make_check_config "$WORK/e.json" "$WORK/e-check.json"
  run_ciano CHECK check-d "$nsd" "$WORK/d-check.json" >/dev/null 2>&1 || fail "CHECK d failed"
  run_ciano CHECK check-e "$nse" "$WORK/e-check.json" >/dev/null 2>&1 || fail "CHECK e failed"
  expect_fail "CHECK with another container netns" "sandbox" \
    run_ciano CHECK check-d "$nse" "$WORK/d-check.json"
  run_ciano DEL check-d "$nsd" "$WORK/conf.json" >/dev/null 2>&1
  run_ciano DEL check-e "$nse" "$WORK/conf.json" >/dev/null 2>&1

  [ -z "$(host_veths)" ] || fail "veths remain after CHECK scenarios"
  [ "$(alloc_count)" -eq 0 ] || fail "allocations remain after CHECK scenarios"

  kill "$pd" "$pe" 2>/dev/null || true
  echo "PASS check"
}

case "$SCENARIO" in
  all)
    scenario_add_del
    scenario_del_edge
    scenario_check
    echo "ALL PASS"
    ;;
  add-del) scenario_add_del ;;
  del-edge) scenario_del_edge ;;
  check) scenario_check ;;
esac
