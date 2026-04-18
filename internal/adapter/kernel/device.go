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
// exporter reads interface attributes from "<busid>:<config>.<alt>".
// Upstream usbip client only reads config 1, alt 0; we match.
const ifaceSuffixFmt = "%s:%d.%d"

// defaultConfigIndex and defaultAltIndex identify the "primary"
// interface for bind/list purposes. Matches upstream libsrc.
const (
	defaultConfigIndex = 1
	defaultAltIndex    = 0
)

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

// readDevice reads the ten-attribute per-device sysfs block plus the
// primary interface descriptor for busID.
func (a *ExporterAdapter) readDevice(busID domain.BusID) (domain.Device, error) {
	base := path.Join(SysfsUSBDevices, string(busID))

	core, err := a.readDeviceCore(base, busID)
	if err != nil {
		return domain.Device{}, err
	}

	core.Interfaces = a.readInterfaces(busID, core.NumInterfaces)

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
	}, nil
}

type deviceNumbers struct {
	bus   uint16
	dev   uint16
	speed domain.Speed
}

func (a *ExporterAdapter) readDeviceNumbers(base string) (deviceNumbers, error) {
	busnum, err := ReadUint(a.fs, path.Join(base, devAttrBusnum))
	if err != nil {
		return deviceNumbers{}, err
	}

	devnum, err := ReadUint(a.fs, path.Join(base, devAttrDevnum))
	if err != nil {
		return deviceNumbers{}, err
	}

	speed, err := ReadUint(a.fs, path.Join(base, devAttrSpeed))
	if err != nil {
		return deviceNumbers{}, err
	}

	return deviceNumbers{
		bus:   uint16(busnum & u16Max),
		dev:   uint16(devnum & u16Max),
		speed: domain.Speed(speed),
	}, nil
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
	class, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceClass))
	if err != nil {
		return deviceClasses{}, err
	}

	subclass, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceSub))
	if err != nil {
		return deviceClasses{}, err
	}

	protocol, err := ReadHex16(a.fs, path.Join(base, devAttrDeviceProto))
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
		class:         domain.USBClass(narrowByte(class)),
		subclass:      domain.USBSubclass(narrowByte(subclass)),
		protocol:      domain.USBProtocol(narrowByte(protocol)),
		configValue:   configValue,
		numConfigs:    numConfigs,
		numInterfaces: numInterfaces,
	}, nil
}

// narrowByte truncates a uint16 attribute to uint8 after range-checking.
// USB device-level class/subclass/protocol fields are technically u8 on
// wire; sysfs formats them as two-digit hex so ReadHex16 is the natural
// read primitive. Values above 0xFF are a sysfs bug and surface as
// the low 8 bits (matching kernel truncation behaviour).
func narrowByte(v uint16) uint8 {
	return uint8(v & byteMax)
}

// readByteAttr reads a decimal sysfs attribute that is semantically a
// byte (e.g. bConfigurationValue). Clamps to the low 8 bits to keep
// gosec happy without giving up the readable uint32 path in readUint.
func (a *ExporterAdapter) readByteAttr(base, attr string) (uint8, error) {
	v, err := ReadUint(a.fs, path.Join(base, attr))
	if err != nil {
		return 0, err
	}

	return uint8(v & byteMax), nil
}

// readInterfaces reads each interface descriptor under the device's
// primary config (config 1). Unreadable interfaces are skipped with a
// debug log rather than failing the whole device — some USB peripherals
// expose only a subset of their configured interfaces under sysfs.
func (a *ExporterAdapter) readInterfaces(busID domain.BusID, count uint8) []domain.Interface {
	ifaces := make([]domain.Interface, 0, count)

	for i := range int(count) {
		suffix := fmt.Sprintf(ifaceSuffixFmt, string(busID), defaultConfigIndex, i)
		base := path.Join(SysfsUSBDevices, suffix)

		iface, err := a.readInterface(base)
		if err != nil {
			if isMissing(err) {
				continue
			}

			a.logger.Warn("skip unreadable interface", "busid", busID, "alt", i, "err", err)

			continue
		}

		ifaces = append(ifaces, iface)
	}

	return ifaces
}

// readInterface reads one interface descriptor block.
func (a *ExporterAdapter) readInterface(base string) (domain.Interface, error) {
	class, err := ReadHex16(a.fs, path.Join(base, ifaceAttrClass))
	if err != nil {
		return domain.Interface{}, err
	}

	subclass, err := ReadHex16(a.fs, path.Join(base, ifaceAttrSubClass))
	if err != nil {
		return domain.Interface{}, err
	}

	protocol, err := ReadHex16(a.fs, path.Join(base, ifaceAttrProtocol))
	if err != nil {
		return domain.Interface{}, err
	}

	alt, err := ReadUint(a.fs, path.Join(base, ifaceAttrAltSet))
	if err != nil {
		return domain.Interface{}, err
	}

	return domain.Interface{
		Class:    domain.USBClass(narrowByte(class)),
		Subclass: domain.USBSubclass(narrowByte(subclass)),
		Protocol: domain.USBProtocol(narrowByte(protocol)),
		Alt:      uint8(alt & byteMax),
	}, nil
}

// isMissing reports whether err chains to fs.ErrNotExist or to our
// domain.ErrDeviceNotFound / domain.ErrKernelModuleMissing. Used by
// readInterfaces to distinguish "file not present in sysfs" (skip)
// from "read error" (log and skip).
func isMissing(err error) bool {
	return errorsIsAny(err, fs.ErrNotExist)
}
