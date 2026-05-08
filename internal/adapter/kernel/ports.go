//go:build linux

package kernel

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// statusFieldCount is the number of whitespace-separated fields in a
// vhci status row: hub, port, sta, spd, dev, sockfd, local_busid.
const statusFieldCount = 7

// errStatusRowControllerMismatch surfaces when a parsed row's declared
// flat port does not belong to the controller block of the status file
// it came from (row.port / VHCIPorts != controllerIdx). The kernel
// writes status.<N> with flat ports confined to the [N*VHCIPorts,
// (N+1)*VHCIPorts) window; a row outside that window means the sysfs
// state is inconsistent and any downstream attach/detach math keyed on
// it would be wrong.
var errStatusRowControllerMismatch = errors.New("status row's flat port does not belong to its controller file")

// Indexes into a split status-row line.
const (
	rowIdxHub     = 0
	rowIdxPort    = 1
	rowIdxStatus  = 2
	rowIdxSpeed   = 3
	rowIdxDevID = 4
	// column 5 is the sockfd; we never need it on the read path — the
	// kernel owns the fd once attach succeeds, so we skip straight to 6.
	rowIdxBusID = 6
	statusRadix10 = 10
	statusRadix16 = 16
	statusBits32  = 32
)

// parsedPort is the intermediate form of a row before mapping into a
// domain.Port. Split from the public type so the parser can track
// whether the row was free (status == 0) or used.
type parsedPort struct {
	hub    string
	port   domain.PortID
	status domain.Status
	speed  domain.Speed
	devID  domain.DeviceID
	busID  domain.BusID
}

// readStatusRows parses every status + status.N file into parsedPort
// rows. Controller count and per-controller VHCI_PORTS width come from
// the cached topology snapshot — the kernel already writes the fully
// flat port identifier in each row, so the parser must trust that
// value verbatim and use VHCIPorts only to validate that a row
// belongs to the controller file it was read from.
//
// Malformed rows surface a slog.Warn signal and are skipped; a row
// whose flat port falls outside its controller's window fails the
// whole call — that is a kernel-state inconsistency the caller must
// see, not a tokenisation glitch the caller can ignore.
func (a *commonAdapter) readStatusRows() ([]parsedPort, error) {
	topo, err := a.loadTopology()
	if err != nil {
		return nil, err
	}

	rows := make([]parsedPort, 0)

	for i := range topo.NControllers {
		fileName := statusFileName(i)

		raw, rerr := readFileBytes(a.fs, path.Join(SysfsVHCIHCD, fileName))
		if rerr != nil {
			return nil, rerr
		}

		parsed, perr := a.parseStatusFile(string(raw), fileName, i, topo.VHCIPorts)
		if perr != nil {
			return nil, perr
		}

		rows = append(rows, parsed...)
	}

	return rows, nil
}

// statusFileName maps a controller index to the status file's
// basename. Controller 0 uses "status"; subsequent controllers use
// "status.N".
func statusFileName(idx uint32) string {
	if idx == 0 {
		return SysfsVHCIStatus
	}

	return fmt.Sprintf(SysfsVHCIStatusFmt, idx)
}

// parseStatusFile tokenises every line of a status file. Malformed
// rows surface a warn log and are skipped; a row whose flat port does
// not belong to controllerIdx's block ([controllerIdx*vhciPorts,
// (controllerIdx+1)*vhciPorts)) is a kernel-state inconsistency and
// fails the whole call. vhciPorts is the per-controller width
// (VHCI_PORTS, i.e. HCPorts*2) sourced from the cached topology.
func (a *commonAdapter) parseStatusFile(body, source string, controllerIdx, vhciPorts uint32) ([]parsedPort, error) {
	out := make([]parsedPort, 0)

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := scanner.Text()
		if skipStatusLine(line) {
			continue
		}

		row, ok := parseStatusRow(line)
		if !ok {
			a.logger.Warn("skip malformed vhci status row",
				"source", source, "line", line)

			continue
		}

		if uint32(row.port)/vhciPorts != controllerIdx {
			return nil, fmt.Errorf("%w: source=%s port=%d controllerIdx=%d vhciPorts=%d",
				errStatusRowControllerMismatch, source, uint32(row.port), controllerIdx, vhciPorts)
		}

		out = append(out, row)
	}

	return out, nil
}

// skipStatusLine reports whether line should be ignored during row
// parsing: empty lines, whitespace-only lines, and the optional
// header. The header test matches by the leading "hub" token; the
// kernel emits exactly one header line and never uses "hub" as a
// valid first token on a data row ("hs" or "ss" are the valid tokens).
func skipStatusLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return true
	}

	return fields[0] == VHCIStatusHeaderPrefix
}

// parseStatusRow applies the sscanf format "%s  %04u %03u %03u %08x %06u %s"
// to line. Returns ok=false on any tokenisation or parse failure.
//
// The port column in a vhci status row is already the flat port
// identifier (see status_show_vhci in drivers/usb/usbip/vhci_sysfs.c:
// flat = pdev_nr*VHCI_PORTS + hubOffset + rhport). The parser therefore
// emits the value verbatim and leaves cross-file range validation to
// parseStatusFile.
func parseStatusRow(line string) (parsedPort, bool) {
	fields := strings.Fields(line)
	if len(fields) != statusFieldCount {
		return parsedPort{}, false
	}

	hub := fields[rowIdxHub]
	if hub != HubTypeHighSpeed && hub != HubTypeSuperSpeed {
		return parsedPort{}, false
	}

	nums, ok := parseStatusRowNumbers(fields)
	if !ok {
		return parsedPort{}, false
	}

	return parsedPort{
		hub:    hub,
		port:   domain.PortID(nums.port),
		status: domain.Status(nums.sta),
		speed:  domain.Speed(nums.spd),
		devID:  domain.DeviceID(nums.devID),
		busID:  domain.BusID(fields[rowIdxBusID]),
	}, true
}

// statusNums holds the parsed numeric columns of a status row.
type statusNums struct {
	port  uint32
	sta   uint32
	spd   uint32
	devID uint32
}

// parseStatusRowNumbers extracts the four numeric fields
// (port/status/speed/devid) from a tokenised status row. Split out to
// keep parseStatusRow under the cyclomatic cap.
func parseStatusRowNumbers(fields []string) (statusNums, bool) {
	port, err := strconv.ParseUint(fields[rowIdxPort], statusRadix10, statusBits32)
	if err != nil {
		return statusNums{}, false
	}

	sta, err := strconv.ParseUint(fields[rowIdxStatus], statusRadix10, statusBits32)
	if err != nil {
		return statusNums{}, false
	}

	spd, err := strconv.ParseUint(fields[rowIdxSpeed], statusRadix10, statusBits32)
	if err != nil {
		return statusNums{}, false
	}

	devID, err := strconv.ParseUint(fields[rowIdxDevID], statusRadix16, statusBits32)
	if err != nil {
		return statusNums{}, false
	}

	return statusNums{
		port:  uint32(port),
		sta:   uint32(sta),
		spd:   uint32(spd),
		devID: uint32(devID),
	}, true
}

// findFreePort returns the lowest-numbered unused port whose speed
// class matches the requested speed. USB low/full/high/wireless select
// "hs" rows; USB SuperSpeed / SuperSpeedPlus select "ss" rows.
func (a *commonAdapter) findFreePort(speed domain.Speed) (domain.PortID, error) {
	rows, err := a.readStatusRows()
	if err != nil {
		return 0, err
	}

	desired := hubTypeForSpeed(speed)
	port, ok := lowestFreePort(rows, desired)

	if !ok {
		return 0, fmt.Errorf("%w: speed class %s", domain.ErrNoFreePort, speed)
	}

	return port, nil
}

// lowestFreePort scans rows and returns the lowest-numbered port whose
// hub token matches desired and whose status indicates it is free
// (Null or Available). Split out of findFreePort to keep cognitive
// complexity below the project's cap of 10.
func lowestFreePort(rows []parsedPort, desired string) (domain.PortID, bool) {
	lowest := domain.PortID(^uint32(0))
	found := false

	for _, r := range rows {
		if r.hub != desired {
			continue
		}

		if !isFreeStatus(r.status) {
			continue
		}

		if !found || r.port < lowest {
			lowest = r.port
			found = true
		}
	}

	return lowest, found
}

// isFreeStatus reports whether a row's status represents a port we
// may claim: StatusNull (unused) and StatusAvailable (ready).
func isFreeStatus(s domain.Status) bool {
	return s == domain.StatusNull || s == domain.StatusAvailable
}

// hubTypeForSpeed maps a USB speed to the vhci hub-type token. USB
// 3.x → "ss"; everything else → "hs" (matches upstream libsrc).
func hubTypeForSpeed(speed domain.Speed) string {
	switch speed {
	case domain.SpeedSuper, domain.SpeedSuperPlus:
		return HubTypeSuperSpeed
	case domain.SpeedUnknown, domain.SpeedLow, domain.SpeedFull,
		domain.SpeedHigh, domain.SpeedWireless:
		return HubTypeHighSpeed
	default:
		return HubTypeHighSpeed
	}
}

// ListPorts parses every row of every status file into domain.Port,
// including unused slots. Spec §3.4: when modules are missing, both
// the nil slice AND ErrKernelModuleMissing surface — callers must
// check the error.
func (a *ImporterAdapter) ListPorts(ctx context.Context) ([]domain.Port, error) {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := a.readStatusRows()
	if err != nil {
		return nil, err
	}

	ports := make([]domain.Port, 0, len(rows))
	for _, r := range rows {
		ports = append(ports, domain.Port{
			ID:         r.port,
			Status:     r.status,
			Speed:      r.speed,
			DeviceID:   r.devID,
			BusID:      r.busID,
			LocalBusID: r.busID,
		})
	}

	return ports, nil
}

