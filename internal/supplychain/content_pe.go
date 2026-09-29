package supplychain

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// peHeaderWindow is how much of an image's start the PE probe keeps. The DOS header, the
// PE headers and the section table of an EFI image sit in its first pages, and 64 KiB is
// room for 1,600 section headers after them.
const peHeaderWindow = 64 << 10

// Offsets and sizes the Microsoft PE/COFF specification fixes.
const (
	peDOSHeaderSize      = 0x40
	peNewHeaderOffsetAt  = 0x3c
	peSignatureSize      = 4
	peCOFFHeaderSize     = 20
	peSubsystemAt        = 68
	peSectionHeaderSize  = 40
	pe32Magic            = 0x10b
	pe32PlusMagic        = 0x20b
	peEFIApplication     = 10
	peEFIROM             = 13
	ukiRequiredSection   = ".linux"
	peSignature          = "PE\x00\x00"
	peDOSMagic           = "MZ"
	peOptionalHeaderMin  = peSubsystemAt + 2
	peSectionNameBytes   = 8
	peSectionRawSizeAt   = 16
	peSectionRawOffsetAt = 20
)

// peProbe keeps the start of a PE/COFF image and judges its headers, and for a Unified
// Kernel Image its .linux section.
type peProbe struct {
	seen         int64
	head         window
	requireLinux bool
}

func newPEProbe(requireLinux bool) contentProbe {
	return &peProbe{head: window{buf: make([]byte, peHeaderWindow)}, requireLinux: requireLinux}
}

// Write collects the image's headers from the chunk.
func (p *peProbe) Write(chunk []byte) (int, error) {
	p.head.collect(chunk, p.seen)
	p.seen += int64(len(chunk))
	return len(chunk), nil
}

// peImage is what the check reads from the headers: the subsystem and whether a non-empty
// .linux section exists.
type peImage struct {
	subsystem uint16
	hasLinux  bool
}

// check refuses bytes that are not a complete EFI PE/COFF image, and for a Unified Kernel
// Image an image without the .linux section the UAPI UKI specification requires.
func (p *peProbe) check(size int64) error {
	image, err := parsePEImage(p.head.bytes(), size)
	if err != nil {
		return err
	}
	if image.subsystem < peEFIApplication || image.subsystem > peEFIROM {
		return fmt.Errorf("its PE subsystem is %d, not an EFI application, driver or ROM (%d to %d)", image.subsystem, peEFIApplication, peEFIROM)
	}
	if p.requireLinux && !image.hasLinux {
		return fmt.Errorf("the EFI image has no %s section, which every Unified Kernel Image carries and a bare EFI-stub kernel lacks", ukiRequiredSection)
	}
	return nil
}

// peHeaders is a view of the kept image start and the file's size, for bounds checks.
type peHeaders struct {
	head []byte
	size int64
}

// need refuses a header part that the file does not hold in full, or that lies past the
// kept image start.
func (h peHeaders) need(at, length int64, part string) error {
	switch {
	case at < 0 || at+length > h.size:
		return fmt.Errorf("the file ends at byte %d, before the end of the %s at offset %d", h.size, part, at)
	case at+length > int64(len(h.head)):
		return fmt.Errorf("the %s at offset %d lies past the first %d bytes the check reads", part, at, len(h.head))
	default:
		return nil
	}
}

func (h peHeaders) u16(at int64) uint16 { return binary.LittleEndian.Uint16(h.head[at:]) }
func (h peHeaders) u32(at int64) int64  { return int64(binary.LittleEndian.Uint32(h.head[at:])) }

// parsePEImage reads the DOS header, the PE signature, the COFF and optional headers and
// the section table, refusing any part that is missing, cut off or malformed.
func parsePEImage(head []byte, size int64) (peImage, error) {
	h := peHeaders{head: head, size: size}
	if err := h.need(0, peDOSHeaderSize, "DOS header"); err != nil {
		return peImage{}, err
	}
	if !bytes.HasPrefix(head, []byte(peDOSMagic)) {
		return peImage{}, fmt.Errorf("the file does not start with the %q DOS header magic of a PE/COFF image", peDOSMagic)
	}
	signatureAt := h.u32(peNewHeaderOffsetAt)
	if err := h.need(signatureAt, peSignatureSize+peCOFFHeaderSize, "PE signature and COFF header"); err != nil {
		return peImage{}, err
	}
	if string(head[signatureAt:signatureAt+peSignatureSize]) != peSignature {
		return peImage{}, fmt.Errorf("no PE signature at offset %d, where the DOS header points", signatureAt)
	}
	coffAt := signatureAt + peSignatureSize
	subsystem, sectionsAt, err := h.optionalHeader(coffAt)
	if err != nil {
		return peImage{}, err
	}
	hasLinux, err := h.sections(sectionsAt, int64(h.u16(coffAt+2)))
	if err != nil {
		return peImage{}, err
	}
	return peImage{subsystem: subsystem, hasLinux: hasLinux}, nil
}

// optionalHeader checks the optional header after the COFF header at coffAt and returns the
// subsystem it names and the offset of the section table after it.
func (h peHeaders) optionalHeader(coffAt int64) (subsystem uint16, sectionsAt int64, err error) {
	optionalAt := coffAt + peCOFFHeaderSize
	optionalSize := int64(h.u16(coffAt + 16))
	if optionalSize < peOptionalHeaderMin {
		return 0, 0, fmt.Errorf("the PE optional header is %d bytes, too short to name a subsystem", optionalSize)
	}
	if err := h.need(optionalAt, optionalSize, "PE optional header"); err != nil {
		return 0, 0, err
	}
	if magic := h.u16(optionalAt); magic != pe32Magic && magic != pe32PlusMagic {
		return 0, 0, fmt.Errorf("the PE optional header magic is %#x, neither PE32 (%#x) nor PE32+ (%#x)", magic, pe32Magic, pe32PlusMagic)
	}
	return h.u16(optionalAt + peSubsystemAt), optionalAt + optionalSize, nil
}

// sections checks that the section table at tableAt and every section's raw data lie inside
// the file, and reports whether a non-empty .linux section is among them.
func (h peHeaders) sections(tableAt, count int64) (hasLinux bool, err error) {
	if count == 0 {
		return false, fmt.Errorf("the PE image declares no sections")
	}
	if err := h.need(tableAt, count*peSectionHeaderSize, "PE section table"); err != nil {
		return false, err
	}
	for i := int64(0); i < count; i++ {
		entry := tableAt + i*peSectionHeaderSize
		name := string(bytes.TrimRight(h.head[entry:entry+peSectionNameBytes], "\x00"))
		rawSize, rawAt := h.u32(entry+peSectionRawSizeAt), h.u32(entry+peSectionRawOffsetAt)
		if rawSize > 0 && rawAt+rawSize > h.size {
			return false, fmt.Errorf("PE section %q ends at byte %d, past the end of the %d-byte file", name, rawAt+rawSize, h.size)
		}
		hasLinux = hasLinux || (name == ukiRequiredSection && rawSize > 0)
	}
	return hasLinux, nil
}
