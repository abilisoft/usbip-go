#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 AbiliSoft
# SPDX-License-Identifier: Apache-2.0
#
# capture-wire-fixtures.sh — capture real upstream USBIP wire frames for
# the internal/adapter/wire fixture set. Replaces the hand-synthesised
# fixtures from the initial codec phase with ground-truth bytes
# emitted by upstream usbip-utils + a usbip-vudc virtual device.
#
# Requires sudo for:
#   - modprobe (load usbip_core / usbip_host / vhci_hcd / usbip_vudc)
#   - configfs writes under /sys/kernel/config
#   - sysfs writes to bind the vudc device via usbip-host
#   - starting usbipd
#   - tcpdump with CAP_NET_RAW
#
# Outputs:
#   - internal/adapter/wire/testdata/real_op_req_devlist.bin
#   - internal/adapter/wire/testdata/real_op_rep_devlist_1.bin
#   - internal/adapter/wire/testdata/real_op_req_import.bin
#   - internal/adapter/wire/testdata/real_op_rep_import.bin
#   - internal/adapter/wire/testdata/real_capture.pcap (full trace for audit)
#   - internal/adapter/wire/testdata/real_capture.manifest.json (what each file contains, env, versions)
#
# Usage: run from the repo root. Destructive ONLY within /sys/kernel/config/usb_gadget/usbip-fixture-vudc
# and the transient tcpdump capture file. Cleans up on exit via trap.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TESTDATA_DIR="${REPO_ROOT}/internal/adapter/wire/testdata"
CAPTURE_PCAP="${TESTDATA_DIR}/real_capture.pcap"
MANIFEST="${TESTDATA_DIR}/real_capture.manifest.json"

VUDC_NAME="usbip-fixture-vudc"
VUDC_ROOT="/sys/kernel/config/usb_gadget/${VUDC_NAME}"
VUDC_VID="0x0951"    # Kingston
VUDC_PID="0x1666"    # DataTraveler 100 G3
VUDC_BCD="0x0110"

USBIPD_PORT=3240
USBIPD_PIDFILE="/tmp/usbip-fixture-usbipd.pid"
TCPDUMP_PIDFILE="/tmp/usbip-fixture-tcpdump.pid"

UPSTREAM_USBIP="$(command -v usbip || echo /usr/bin/usbip)"
UPSTREAM_USBIPD="$(command -v usbipd || echo /usr/bin/usbipd)"

# ----------------------------------------------------------------------
# Preflight
# ----------------------------------------------------------------------

if [[ "${EUID}" -eq 0 ]]; then
  echo "run without sudo; the script prompts for it per-operation" >&2
  exit 2
fi

for bin in tcpdump tshark sudo modprobe; do
  command -v "${bin}" >/dev/null 2>&1 || { echo "missing: ${bin}" >&2; exit 2; }
done

[[ -x "${UPSTREAM_USBIP}" ]]  || { echo "missing usbip at ${UPSTREAM_USBIP}" >&2; exit 2; }
[[ -x "${UPSTREAM_USBIPD}" ]] || { echo "missing usbipd at ${UPSTREAM_USBIPD}" >&2; exit 2; }

mkdir -p "${TESTDATA_DIR}"

USBIP_VERSION_STRING="$("${UPSTREAM_USBIP}" version 2>/dev/null | head -1 || true)"
[[ -z "${USBIP_VERSION_STRING}" ]] && USBIP_VERSION_STRING="unknown"

echo "==> capturing upstream wire fixtures"
echo "    usbip:  ${UPSTREAM_USBIP} (${USBIP_VERSION_STRING})"
echo "    usbipd: ${UPSTREAM_USBIPD}"
echo "    kernel: $(uname -r)"
echo "    testdata: ${TESTDATA_DIR}"

# ----------------------------------------------------------------------
# Cleanup trap
# ----------------------------------------------------------------------

cleanup() {
  local rc=$?
  set +e
  echo "==> cleanup"

  if [[ -f "${TCPDUMP_PIDFILE}" ]]; then
    sudo kill "$(cat "${TCPDUMP_PIDFILE}")" 2>/dev/null
    sudo rm -f "${TCPDUMP_PIDFILE}"
  fi

  if [[ -f "${USBIPD_PIDFILE}" ]]; then
    sudo kill "$(cat "${USBIPD_PIDFILE}")" 2>/dev/null
    sudo rm -f "${USBIPD_PIDFILE}"
  fi

  # Unbind UDC first (writing empty string detaches the gadget).
  if [[ -f "${VUDC_ROOT}/UDC" ]]; then
    echo "" | sudo tee "${VUDC_ROOT}/UDC" >/dev/null 2>&1 || true
  fi

  # Remove symlinks from configs/*/ (function links must go before rmdir).
  if [[ -d "${VUDC_ROOT}" ]]; then
    sudo find "${VUDC_ROOT}/configs" -type l -print 2>/dev/null | sudo xargs -r rm -f
    # configfs dirs must be removed depth-first. find -depth guarantees
    # children are processed before parents; leaf dirs (e.g.
    # configs/c.1/strings/0x409) vanish first so their parents can
    # subsequently rmdir.
    sudo find "${VUDC_ROOT}" -mindepth 1 -depth -type d -exec rmdir {} \; 2>/dev/null
    sudo rmdir "${VUDC_ROOT}" 2>/dev/null
  fi

  # Remove any temp backing files.
  rm -f /tmp/usbip-fixture-*.img 2>/dev/null

  exit "${rc}"
}
trap cleanup EXIT

# ----------------------------------------------------------------------
# Step 1 — load kernel modules
# ----------------------------------------------------------------------

echo "==> loading kernel modules"
# libcomposite provides configfs usb_gadget; usb_f_mass_storage provides the
# function we attach (empty gadgets cannot bind to a UDC).
for mod in usbip_core vhci_hcd usbip_host usbip_vudc libcomposite usb_f_mass_storage; do
  if [[ -d "/sys/module/${mod}" ]]; then
    echo "    ${mod} already loaded"
  else
    sudo modprobe "${mod}"
    echo "    ${mod} loaded"
  fi
done

# ----------------------------------------------------------------------
# Step 2 — mount configfs if needed and create vudc device
# ----------------------------------------------------------------------

if ! mountpoint -q /sys/kernel/config; then
  echo "==> mounting configfs"
  sudo mount -t configfs none /sys/kernel/config
fi

if [[ ! -d /sys/kernel/config/usb_gadget ]]; then
  echo "configfs does not expose usb_gadget after libcomposite modprobe" >&2
  exit 2
fi

echo "==> creating vudc gadget"
sudo mkdir -p "${VUDC_ROOT}"
echo "${VUDC_VID}" | sudo tee "${VUDC_ROOT}/idVendor" >/dev/null
echo "${VUDC_PID}" | sudo tee "${VUDC_ROOT}/idProduct" >/dev/null
echo "${VUDC_BCD}" | sudo tee "${VUDC_ROOT}/bcdDevice" >/dev/null
sudo mkdir -p "${VUDC_ROOT}/strings/0x409"
echo "abilisoft-fixture" | sudo tee "${VUDC_ROOT}/strings/0x409/serialnumber" >/dev/null
echo "usbip-go"          | sudo tee "${VUDC_ROOT}/strings/0x409/manufacturer"  >/dev/null
echo "Fixture Device"    | sudo tee "${VUDC_ROOT}/strings/0x409/product"       >/dev/null

sudo mkdir -p "${VUDC_ROOT}/configs/c.1/strings/0x409"
echo "default" | sudo tee "${VUDC_ROOT}/configs/c.1/strings/0x409/configuration" >/dev/null

# Add a mass_storage function backed by a tiny in-RAM file. Empty gadgets
# are rejected by the UDC with EBUSY — the function just needs to exist
# and be linked into configs/c.1.
MS_BACKING="$(mktemp /tmp/usbip-fixture-msd.XXXXXX.img)"
dd if=/dev/zero of="${MS_BACKING}" bs=1M count=1 status=none
sudo chmod 0644 "${MS_BACKING}"
sudo mkdir -p "${VUDC_ROOT}/functions/mass_storage.0"
echo "${MS_BACKING}" | sudo tee "${VUDC_ROOT}/functions/mass_storage.0/lun.0/file" >/dev/null
sudo ln -sf "${VUDC_ROOT}/functions/mass_storage.0" "${VUDC_ROOT}/configs/c.1/mass_storage.0"

# Bind to the first available usbip-vudc UDC instance.
UDC_INSTANCE=$(ls /sys/class/udc | grep -E '^usbip-vudc' | head -1 || true)
if [[ -z "${UDC_INSTANCE}" ]]; then
  echo "no usbip-vudc UDC instance available; module-param vudc_num may need bumping" >&2
  exit 2
fi
echo "==> binding gadget to ${UDC_INSTANCE}"
echo "${UDC_INSTANCE}" | sudo tee "${VUDC_ROOT}/UDC" >/dev/null

# ----------------------------------------------------------------------
# Step 3 — in vudc mode the gadget-to-UDC write above already makes the
# device exportable (usbip_status flips to "1"). No `usbip bind` needed.
# The busid advertised on the wire is the platform device name.
# ----------------------------------------------------------------------

VUDC_BUSID="${UDC_INSTANCE}"
echo "==> vudc busid: ${VUDC_BUSID}"

# ----------------------------------------------------------------------
# Step 4 — start tcpdump then usbipd in device (vudc) mode
# ----------------------------------------------------------------------

echo "==> starting tcpdump (loopback, port ${USBIPD_PORT})"
sudo tcpdump -i lo -w "${CAPTURE_PCAP}" "tcp port ${USBIPD_PORT}" &
echo $! | sudo tee "${TCPDUMP_PIDFILE}" >/dev/null
sleep 1

echo "==> starting usbipd -e (device/vudc mode)"
USBIPD_LOG="/tmp/usbip-fixture-usbipd.log"
# Refuse to proceed if another usbipd is already using port 3240 — we
# must capture bytes from a usbipd we ourselves launched in vudc mode,
# not a coincidentally-running host-mode daemon from some other setup.
if sudo ss -Hlnt "sport = :${USBIPD_PORT}" | grep -q .; then
  echo "port ${USBIPD_PORT} already in use; refusing to capture against a foreign daemon" >&2
  sudo ss -Hlnt "sport = :${USBIPD_PORT}" >&2
  exit 2
fi

# usbipd's --pid uses optional_argument, so "-P <file>" or "-PP <file>"
# (space-separated) parses as "-P" (default pidfile) plus a stray
# positional and exits silently. Use the long form with = instead. If
# that invocation fails or does not write the pidfile, abort — do NOT
# fall back to pgrep, because a pre-existing rogue usbipd would poison
# the capture.
if ! sudo "${UPSTREAM_USBIPD}" -D -e "--pid=${USBIPD_PIDFILE}" 2>"${USBIPD_LOG}"; then
  echo "usbipd invocation failed; log:" >&2
  cat "${USBIPD_LOG}" >&2 2>/dev/null || true
  exit 2
fi
sleep 2

if [[ ! -f "${USBIPD_PIDFILE}" ]]; then
  echo "usbipd daemonised but did not write ${USBIPD_PIDFILE}; log:" >&2
  cat "${USBIPD_LOG}" >&2 2>/dev/null || true
  exit 2
fi
echo "    usbipd pid: $(cat "${USBIPD_PIDFILE}")"

# ----------------------------------------------------------------------
# Step 5 — drive the client so the capture includes OP_REQ_DEVLIST,
# OP_REP_DEVLIST, OP_REQ_IMPORT, and OP_REP_IMPORT.
# ----------------------------------------------------------------------

# `usbip list` must succeed end-to-end; a silent failure here would let
# a malformed OP_REP_DEVLIST pass downstream as fixture truth.
echo "==> client: usbip list -r 127.0.0.1"
"${UPSTREAM_USBIP}" list -r 127.0.0.1
sleep 1

# Attach is the one step where a non-zero exit is expected and does NOT
# mean the wire exchange failed: upstream's attach writes the socket fd
# to vhci_hcd AFTER receiving OP_REP_IMPORT, and that handoff fails on
# a self-attach against a vudc we own. Byte-level validity is enforced
# by the size checks after extraction (§ Step 6). Capture BOTH stdout
# and stderr so an unexpected earlier failure (e.g. protocol mismatch)
# still surfaces as a size-check abort rather than silently stamping
# partial bytes as fixture truth.
echo "==> client: usbip attach -r 127.0.0.1 -b ${VUDC_BUSID} (post-handshake vhci handoff is expected to fail)"
"${UPSTREAM_USBIP}" attach -r 127.0.0.1 -b "${VUDC_BUSID}" \
  > /tmp/usbip-fixture-attach.log 2>&1 || true
sleep 1

# ----------------------------------------------------------------------
# Step 6 — stop capture; extract raw TCP payloads per direction
# ----------------------------------------------------------------------

echo "==> stopping usbipd + tcpdump"
sudo kill "$(cat "${USBIPD_PIDFILE}")" 2>/dev/null
sudo kill "$(cat "${TCPDUMP_PIDFILE}")" 2>/dev/null
sleep 1

# Use tshark to extract the raw TCP payload bytes in hex per packet,
# then pick out the first packet in each direction to split into REQ/REP
# files. This is the hands-on part — adjust stream indices if the
# capture has more than one session (the trap in this script means only
# one client session should be present).
echo "==> extracting payloads from ${CAPTURE_PCAP}"

# `usbip list` and `usbip attach` are separate invocations and therefore
# open separate TCP connections (= separate tshark streams). A single
# USBIP message can span multiple TCP segments, so we must concatenate
# every segment within a (stream, direction) pair rather than taking
# the first one.

mapfile -t STREAMS < <(tshark -r "${CAPTURE_PCAP}" \
  -Y "tcp.port == ${USBIPD_PORT} && tcp.len > 0" \
  -T fields -e tcp.stream | sort -un)

if [[ "${#STREAMS[@]}" -lt 2 ]]; then
  echo "expected at least 2 TCP streams on port ${USBIPD_PORT}; got ${#STREAMS[@]}" >&2
  exit 2
fi

extract_stream() {
  local stream="$1"
  local direction="$2"
  local filter
  case "${direction}" in
    req) filter="tcp.stream == ${stream} && tcp.dstport == ${USBIPD_PORT} && tcp.len > 0" ;;
    rep) filter="tcp.stream == ${stream} && tcp.srcport == ${USBIPD_PORT} && tcp.len > 0" ;;
    *)   echo "bad direction: ${direction}" >&2; return 2 ;;
  esac
  tshark -r "${CAPTURE_PCAP}" -Y "${filter}" -T fields -e tcp.payload \
    | tr -d '\n:'
}

# First stream = `usbip list` (devlist). Second stream = `usbip attach` (import).
REQ_LIST_HEX="$(extract_stream "${STREAMS[0]}" req)"
REP_LIST_HEX="$(extract_stream "${STREAMS[0]}" rep)"
REQ_IMPORT_HEX="$(extract_stream "${STREAMS[1]}" req)"
REP_IMPORT_HEX="$(extract_stream "${STREAMS[1]}" rep)"

hex_to_bin() {
  local hex="$1"
  local out="$2"
  printf '%s' "${hex}" | xxd -r -p > "${out}"
}

hex_to_bin "${REQ_LIST_HEX}"   "${TESTDATA_DIR}/real_op_req_devlist.bin"
hex_to_bin "${REP_LIST_HEX}"   "${TESTDATA_DIR}/real_op_rep_devlist_1.bin"
hex_to_bin "${REQ_IMPORT_HEX}" "${TESTDATA_DIR}/real_op_req_import.bin"
hex_to_bin "${REP_IMPORT_HEX}" "${TESTDATA_DIR}/real_op_rep_import.bin"

# Enforce exact on-wire sizes per spec §6.2. A size mismatch means the
# capture window caught either too little (truncated TCP) or too much
# (post-handshake URB bytes concatenated onto the reply), and either
# case must abort — a warn would let corrupt bytes land as fixture truth.
req_devlist_sz=$(stat -c%s "${TESTDATA_DIR}/real_op_req_devlist.bin")
req_import_sz=$(stat -c%s "${TESTDATA_DIR}/real_op_req_import.bin")
rep_import_sz=$(stat -c%s "${TESTDATA_DIR}/real_op_rep_import.bin")
rep_devlist_sz=$(stat -c%s "${TESTDATA_DIR}/real_op_rep_devlist_1.bin")

size_fail=0
abort_on_size() {
  local name="$1" got="$2" want="$3"
  echo "FATAL: ${name} = ${got}B (expected ${want})" >&2
  size_fail=1
}

[[ "${req_devlist_sz}" -eq 8 ]]   || abort_on_size OP_REQ_DEVLIST   "${req_devlist_sz}" 8
[[ "${req_import_sz}" -eq 40 ]]   || abort_on_size OP_REQ_IMPORT    "${req_import_sz}"  40
# OP_REP_IMPORT is 320 on success (status=0 + 312-byte body) or exactly
# 8 on error (header-only with non-zero status). Anything else means
# the capture either truncated the body or tacked on extra URB traffic.
case "${rep_import_sz}" in
  8|320) ;;
  *) abort_on_size OP_REP_IMPORT "${rep_import_sz}" "8 (error) or 320 (success)" ;;
esac
# OP_REP_DEVLIST minimum for one device with zero interfaces = 324.
# Upper bound depends on bNumInterfaces of the advertised device, so
# accept >= 324 but bail on anything unreasonable (>1 MiB is certainly
# not a single handshake reply).
if [[ "${rep_devlist_sz}" -lt 324 || "${rep_devlist_sz}" -gt 1048576 ]]; then
  abort_on_size OP_REP_DEVLIST "${rep_devlist_sz}" ">=324 and <=1MiB"
fi

[[ "${size_fail}" -eq 0 ]] || exit 2

# ----------------------------------------------------------------------
# Step 7 — manifest
# ----------------------------------------------------------------------

cat > "${MANIFEST}" <<JSON
{
  "captured_at":   "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "kernel":        "$(uname -r)",
  "usbip_version": "$(${UPSTREAM_USBIP} --version 2>&1 | head -1 || echo unknown)",
  "usbipd_path":   "${UPSTREAM_USBIPD}",
  "vudc": {
    "busid":       "${VUDC_BUSID}",
    "idVendor":    "${VUDC_VID}",
    "idProduct":   "${VUDC_PID}",
    "bcdDevice":   "${VUDC_BCD}",
    "product":     "Fixture Device",
    "manufacturer": "usbip-go",
    "serial":      "abilisoft-fixture"
  },
  "files": {
    "real_op_req_devlist.bin":   "OP_REQ_DEVLIST from client, $(stat -c%s "${TESTDATA_DIR}/real_op_req_devlist.bin") bytes",
    "real_op_rep_devlist_1.bin": "OP_REP_DEVLIST with one bound vudc device, $(stat -c%s "${TESTDATA_DIR}/real_op_rep_devlist_1.bin") bytes",
    "real_op_req_import.bin":    "OP_REQ_IMPORT for the vudc busid, $(stat -c%s "${TESTDATA_DIR}/real_op_req_import.bin") bytes",
    "real_op_rep_import.bin":    "OP_REP_IMPORT success reply, $(stat -c%s "${TESTDATA_DIR}/real_op_rep_import.bin") bytes",
    "real_capture.pcap":         "full tcpdump of the session, for audit"
  }
}
JSON

# ----------------------------------------------------------------------
# Step 8 — drop temp pcap extracts; vudc teardown happens in the cleanup
# trap (unbinding the UDC detaches the gadget cleanly).
# ----------------------------------------------------------------------

rm -f "${TESTDATA_DIR}/real_capture.tshark.tsv"
rm -f "${TESTDATA_DIR}/real_capture.directed.tsv"

echo "==> done. Fixtures in ${TESTDATA_DIR}:"
ls -la "${TESTDATA_DIR}" | grep -E '^-.*real_' | sed 's/^/    /'
echo ""
echo "Next step: run the codec tests against the real fixtures and diff"
echo "against the synthetic ones to find any byte-level discrepancies:"
echo ""
echo "    cd ${REPO_ROOT}"
echo "    diff <(xxd internal/adapter/wire/testdata/device_hs_kingston.bin) \\"
echo "         <(xxd internal/adapter/wire/testdata/real_op_rep_import.bin | tail -n +2)"
echo ""
echo "If the fixtures differ, review the diff and decide whether the codec"
echo "needs adjustment (spec drift) or whether the synthetic fixtures"
echo "were simply wrong (replace them)."
