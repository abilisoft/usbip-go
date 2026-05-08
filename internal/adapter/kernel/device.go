// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// busIDLikePattern matches the Linux USB topology syntax: one or more
// decimal digits (bus number), a dash, then a dot-separated sequence
// of decimal numbers (port path). The pattern excludes interface nodes
// like "1-0:1.0" and controller stubs like "usb1".
var busIDLikePattern = regexp.MustCompile(`^[0-9]+-[0-9]+(\.[0-9]+)*$`)

// Per-device sysfs attribute filenames used by readDevice. Centralised
// here so a kernel ABI shift only touches one file.
const (
	devAttrIDVendor      = "idVendor"
	devAttrIDProduct     = "idProduct"
	devAttrBcdDevice     = "bcdDevice"
	devAttrBusnum        = "busnum"
	devAttrDevnum        = "devnum"
	devAttrSpeed         = "speed"
	devAttrDeviceClass   = "bDeviceClass"
	devAttrDeviceSub     = "bDeviceSubClass"
	devAttrDeviceProto   = "bDeviceProtocol"
	devAttrConfigValue   = "bConfigurationValue"
	devAttrNumConfigs    = "bNumConfigurations"
	devAttrNumInterfaces = "bNumInterfaces"
	devAttrManufacturer  = "manufacturer"
	devAttrProduct       = "product"

	ifaceAttrClass    = "bInterfaceClass"
	ifaceAttrSubClass = "bInterfaceSubClass"
	ifaceAttrProtocol = "bInterfaceProtocol"
	ifaceAttrAltSet   = "bAlternateSetting"
)

// byteMax is the maximum value of a USB 8-bit descriptor byte. Used to
// guard narrowing conversions.
const byteMax = 0xFF

// u16Max is the maximum value of a uint16. Used to guard narrowing
// conversions from uint32 sysfs reads into uint16 domain fields.
const u16Max = 0xFFFF

// ifaceSuffixFmt is the format of a USB interface sysfs entry. The
// exporter reads interface attributes from
// "<busid>:<config>.<interface>" where <config> is the device's
// currently-active bConfigurationValue (read at runtime by
// readDevice and threaded into readInterfaces) and <interface> is
// the per-config interface index iterated over [0, NumInterfaces).
// The alternate setting (bAlternateSetting) lives inside the
// interface directory as a separate sysfs attribute, not as a
// path component.
const ifaceSuffixFmt = "%s:%d.%d"

// defaultConfigIndex is the fallback configuration index used only
// when sysfs reports bConfigurationValue=0 (an unconfigured device).
// readInterfaces uses the device's actual bConfigurationValue
// otherwise; the bind path uses ifaceSuffix which reads
// bConfigurationValue dynamically too.
const defaultConfigIndex = 1

// isBusIDLike reports whether name looks like a USB bus-device topology
// path (e.g. "1-1", "1-1.2.3"). Interface nodes and controller stubs
// are excluded by shape.
func isBusIDLike(name string) bool {
	return busIDLikePattern.MatchString(name)
}

// ListLocalDevices walks /sys/bus/usb/devices, filters for bus-id-like
// entries, and returns one domain.Device per entry. The module
// preflight runs first so a module-loss mid-flight surfaces as
// ErrKernelModuleMissing rather than ErrDeviceNotFound.
func (a *ExporterAdapter) ListLocalDevices(ctx context.Context) ([]domain.Device, error) {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return nil, err
	}

	names, err := ListDirEntries(a.fs, SysfsUSBDevices)
	if err != nil {
		return nil, err
	}

	devices := make([]domain.Device, 0)

	for _, name := range names {
		if !isBusIDLike(name) {
			continue
		}

		d, derr := a.readDevice(domain.BusID(name))
		if derr != nil {
			a.logger.WarnContext(ctx, "skip device with unreadable sysfs attrs",
				"busid", name, "err", derr)

			continue
		}

		devices = append(devices, d)
	}

	return devices, nil
}

// usbipStatusUsed is the SDEV_ST_USED value the kernel writes to
// /sys/bus/usb/devices/<busid>/usbip_status when an importer is
// actively attached. Matches drivers/usb/usbip/stub_dev.h definitions.
const usbipStatusUsed = "2"

// ListExportedDevices returns only devices currently exportable on
// the wire: bound to usbip-host AND not actively claimed by an
// importer. Mirrors upstream usbipd.c::send_reply_devlist (lines
// 172-206) which filters via usbip_host_driver.c::is_my_device()
// and excludes SDEV_ST_USED.
//
// Operators and the CLI's `list -l` continue to use ListLocalDevices
// to see every USB device on the host regardless of bind state. The
// daemon's OP_REP_DEVLIST handler must use THIS method so peers do
// not receive a bus dump including unbound or in-use devices.
func (a *ExporterAdapter) ListExportedDevices(ctx context.Context) ([]domain.Device, error) {
	all, err := a.ListLocalDevices(ctx)
	if err != nil {
		return nil, err
	}

	exported := make([]domain.Device, 0, len(all))

	for _, d := range all {
		if !a.isExportable(d.BusID) {
			continue
		}

		exported = append(exported, d)
	}

	return exported, nil
}

// isExportable reports whether busID is currently bound to usbip-host
// AND its usbip_status is not SDEV_ST_USED. Read errors on either
// attribute are treated as "not exportable" — advertising a device
// whose status we cannot confirm means peers attempt attaches the
// kernel will reject. Better to hide it briefly during rebind and
// re-list once the kernel finishes attaching.
func (a *ExporterAdapter) isExportable(busID domain.BusID) bool {
	driver, err := a.currentDriver(string(busID))
	if err != nil || driver != usbipHostDriverName {
		return false
	}

	status, err := ReadLine(a.fs, path.Join(SysfsUSBDevices, string(busID), SysfsUsbipStatus))
	if err != nil {
		return false
	}

	return status != usbipStatusUsed
}

// readDevice reads the ten-attribute per-device sysfs block plus the
// primary interface descriptor for busID.
func (a *ExporterAdapter) readDevice(busID domain.BusID) (domain.Device, error) {
	base := path.Join(SysfsUSBDevices, string(busID))

	core, err := a.readDeviceCore(base, busID)
	if err != nil {
		return domain.Device{}, err
	}

	ifaces, err := a.readInterfaces(busID, core.ConfigValue, core.NumInterfaces)
	if err != nil {
		return domain.Device{}, err
	}

	core.Interfaces = ifaces

	return core, nil
}

// readDeviceCore populates every domain.Device field that comes from
// per-device sysfs attributes (not the interface descriptors). Kept
// separate from readDevice to hold cyclomatic complexity below the
// project's cap of 10.
func (a *ExporterAdapter) readDeviceCore(base string, busID domain.BusID) (domain.Device, error) {
	vendor, err := ReadHex16(a.fs, path.Join(base, devAttrIDVendor))
	if err != nil {
		return domain.Device{}, err
	}

	product, err := ReadHex16(a.fs, path.Join(base, devAttrIDProduct))
	if err != nil {
		return domain.Device{}, err
	}

	bcd, err := ReadHex16(a.fs, path.Join(base, devAttrBcdDevice))
	if err != nil {
		return domain.Device{}, err
	}

	numbers, err := a.readDeviceNumbers(base)
	if err != nil {
		return domain.Device{}, err
	}

	classes, err := a.readDeviceClasses(base)
	if err != nil {
		return domain.Device{}, err
	}

	manufacturer := readOptionalStringAttr(a.fs, path.Join(base, devAttrManufacturer))
	productName := readOptionalStringAttr(a.fs, path.Join(base, devAttrProduct))

	return domain.Device{
		Path:          base,
		BusID:         busID,
		BusNum:        numbers.bus,
		DevNum:        numbers.dev,
		Speed:         numbers.speed,
		VendorID:      vendor,
		ProductID:     product,
		BcdDevice:     bcd,
		Class:         classes.class,
		Subclass:      classes.subclass,
		Protocol:      classes.protocol,
		ConfigValue:   classes.configValue,
		NumConfigs:    classes.numConfigs,
		NumInterfaces: classes.numInterfaces,
		Manufacturer:  manufacturer,
		Product:       productName,
	}, nil
}

// readOptionalStringAttr reads a sysfs string attribute that the kernel may
// not populate (manufacturer/product are unset when the device descriptor's
// iManufacturer/iProduct index is 0). Missing or unreadable attrs return
// the empty string — they are decorative, not load-bearing.
func readOptionalStringAttr(fsys fs.FS, p string) string {
	s, err := ReadLine(fsys, p)
	if err != nil {
		return ""
	}

	return s
}

type deviceNumbers struct {
	bus   uint16
	dev   uint16
	speed domain.Speed
}

func (a *ExporterAdapter) readDeviceNumbers(base string) (deviceNumbers, error) {
	busnum, err := readU16Attr(a.fs, path.Join(base, devAttrBusnum))
	if err != nil {
		return deviceNumbers{}, err
	}

	devnum, err := readU16Attr(a.fs, path.Join(base, devAttrDevnum))
	if err != nil {
		return deviceNumbers{}, err
	}

	speed, err := ReadSpeedAttr(a.fs, path.Join(base, devAttrSpeed))
	if err != nil {
		return deviceNumbers{}, err
	}

	return deviceNumbers{
		bus:   busnum,
		dev:   devnum,
		speed: speed,
	}, nil
}

// readU16Attr reads a decimal sysfs attribute and validates the value
// fits in uint16. Returns errSysfsValueOutOfRange wrapped on overflow
// so readDevice fails the whole device rather than silently
// truncating.
func readU16Attr(fsys fs.FS, path string) (uint16, error) {
	v, err := ReadUint(fsys, path)
	if err != nil {
		return 0, err
	}

	if v > u16Max {
		return 0, fmt.Errorf("%w: %q = %d (exceeds u16)",
			errSysfsValueOutOfRange, path, v)
	}

	return uint16(v), nil
}

type deviceClasses struct {
	class         domain.USBClass
	subclass      domain.USBSubclass
	protocol      domain.USBProtocol
	configValue   uint8
	numConfigs    uint8
	numInterfaces uint8
}

func (a *ExporterAdapter) readDeviceClasses(base string) (deviceClasses, error) {
	classRaw, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceClass))
	if err != nil {
		return deviceClasses{}, err
	}

	class, err := narrowByteErr(devAttrDeviceClass, classRaw)
	if err != nil {
		return deviceClasses{}, err
	}

	subclassRaw, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceSub))
	if err != nil {
		return deviceClasses{}, err
	}

	subclass, err := narrowByteErr(devAttrDeviceSub, subclassRaw)
	if err != nil {
		return deviceClasses{}, err
	}

	protocolRaw, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceProto))
	if err != nil {
		return deviceClasses{}, err
	}

	protocol, err := narrowByteErr(devAttrDeviceProto, protocolRaw)
	if err != nil {
		return deviceClasses{}, err
	}

	configValue, err := a.readByteAttr(base, devAttrConfigValue)
	if err != nil {
		return deviceClasses{}, err
	}

	numConfigs, err := a.readByteAttr(base, devAttrNumConfigs)
	if err != nil {
		return deviceClasses{}, err
	}

	numInterfaces, err := a.readByteAttr(base, devAttrNumInterfaces)
	if err != nil {
		return deviceClasses{}, err
	}

	return deviceClasses{
		class:         domain.USBClass(class),
		subclass:      domain.USBSubclass(subclass),
		protocol:      domain.USBProtocol(protocol),
		configValue:   configValue,
		numConfigs:    numConfigs,
		numInterfaces: numInterfaces,
	}, nil
}

// narrowByteErr range-checks a uint16 sysfs attribute and returns it
// as uint8. USB device-level class/subclass/protocol fields are u8 on
// wire; sysfs formats them as two-digit hex so ReadHex16 is the
// natural read primitive. Values above 0xFF are a malformed sysfs
// entry and fail the whole device read rather than silently
// truncating.
func narrowByteErr(attr string, v uint16) (uint8, error) {
	if v > byteMax {
		return 0, fmt.Errorf("%w: %q = %d (exceeds u8)",
			errSysfsValueOutOfRange, attr, v)
	}

	return uint8(v), nil
}

// readByteAttr reads a decimal sysfs attribute that is semantically a
// byte (e.g. bConfigurationValue). A value exceeding byteMax is a
// malformed sysfs entry and fails the whole device read rather than
// silently truncating.
func (a *ExporterAdapter) readByteAttr(base, attr string) (uint8, error) {
	p := path.Join(base, attr)

	v, err := ReadUint(a.fs, p)
	if err != nil {
		return 0, err
	}

	if v > byteMax {
		return 0, fmt.Errorf("%w: %q = %d (exceeds u8)",
			errSysfsValueOutOfRange, p, v)
	}

	return uint8(v), nil
}

// readInterfaces reads each interface descriptor under the device's
// CURRENTLY-ACTIVE configuration (the bConfigurationValue passed in
// by readDevice). For an unconfigured device that reports
// configValue=0, falls back to defaultConfigIndex. Missing
// interfaces (ENOENT on optional sysfs attrs) are tolerated — some
// USB peripherals expose only a subset of their configured
// interfaces under sysfs — but overflow errors
// (errSysfsValueOutOfRange) are fatal for the whole device read.
// Surfacing a device with a silently-truncated Interfaces slice when
// sysfs reports malformed byte-width fields would hide data
// corruption from downstream consumers.
func (a *ExporterAdapter) readInterfaces(busID domain.BusID, configValue, count uint8) ([]domain.Interface, error) {
	ifaces := make([]domain.Interface, 0, count)

	// Default to config 1 if the device reports 0 (unconfigured); the
	// interface enumeration would otherwise look for "<busid>:0.<n>"
	// which sysfs does not populate for unconfigured devices anyway.
	cfg := int(configValue)
	if cfg == 0 {
		cfg = defaultConfigIndex
	}

	for i := range int(count) {
		suffix := fmt.Sprintf(ifaceSuffixFmt, string(busID), cfg, i)
		base := path.Join(SysfsUSBDevices, suffix)

		iface, err := a.readInterface(base)
		if err != nil {
			if isMissing(err) {
				continue
			}

			return nil, fmt.Errorf("read interface %s:%d.%d: %w",
				busID, cfg, i, err)
		}

		ifaces = append(ifaces, iface)
	}

	return ifaces, nil
}

// readInterface reads one interface descriptor block.
func (a *ExporterAdapter) readInterface(base string) (domain.Interface, error) {
	classRaw, err := ReadHex16(a.fs, path.Join(base, ifaceAttrClass))
	if err != nil {
		return domain.Interface{}, err
	}

	class, err := narrowByteErr(ifaceAttrClass, classRaw)
	if err != nil {
		return domain.Interface{}, err
	}

	subclassRaw, err := ReadHex16(a.fs, path.Join(base, ifaceAttrSubClass))
	if err != nil {
		return domain.Interface{}, err
	}

	subclass, err := narrowByteErr(ifaceAttrSubClass, subclassRaw)
	if err != nil {
		return domain.Interface{}, err
	}

	protocolRaw, err := ReadHex16(a.fs, path.Join(base, ifaceAttrProtocol))
	if err != nil {
		return domain.Interface{}, err
	}

	protocol, err := narrowByteErr(ifaceAttrProtocol, protocolRaw)
	if err != nil {
		return domain.Interface{}, err
	}

	altRaw, err := ReadUint(a.fs, path.Join(base, ifaceAttrAltSet))
	if err != nil {
		return domain.Interface{}, err
	}

	if altRaw > byteMax {
		return domain.Interface{}, fmt.Errorf("%w: %q = %d (exceeds u8)",
			errSysfsValueOutOfRange, ifaceAttrAltSet, altRaw)
	}

	return domain.Interface{
		Class:    domain.USBClass(class),
		Subclass: domain.USBSubclass(subclass),
		Protocol: domain.USBProtocol(protocol),
		Alt:      uint8(altRaw),
	}, nil
}

// isMissing reports whether err chains to fs.ErrNotExist or to the
// classifier's domain equivalents (domain.ErrDeviceNotFound for device
// paths, domain.ErrKernelModuleMissing for driver/controller/module
// paths). The classifier at classifyENOENT replaces the raw unix.ENOENT
// with a domain-wrapped error that does NOT carry fs.ErrNotExist in
// its chain, so a helper matching only fs.ErrNotExist would miss every
// production ENOENT — absent-interface tolerance in readInterfaces
// (documented above) depended on this helper firing on those wrapped
// chains. Used by readInterfaces to distinguish "file not present in
// sysfs" (skip) from "read error" (fatal).
func isMissing(err error) bool {
	return errorsIsAny(err,
		fs.ErrNotExist,
		domain.ErrDeviceNotFound,
		domain.ErrKernelModuleMissing,
	)
}
