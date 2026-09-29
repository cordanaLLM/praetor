package supplychain

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"
)

// ContentCheck selects what a provenance run does with an artifact whose bytes are not the
// file type its name declares.
type ContentCheck string

const (
	// ContentCheckEnforce refuses the whole statement. The zero value means the same.
	ContentCheckEnforce ContentCheck = "enforce"
	// ContentCheckReport attests the artifact anyway and reports its content as unverified.
	ContentCheckReport ContentCheck = "report"
)

// ParseContentCheck reads a content check mode; an empty value is ContentCheckEnforce.
func ParseContentCheck(value string) (ContentCheck, error) {
	mode := ContentCheck(value)
	if err := mode.validate(); err != nil {
		return "", err
	}
	if mode == "" {
		return ContentCheckEnforce, nil
	}
	return mode, nil
}

// validate refuses a mode other than enforce, report or the zero value.
func (c ContentCheck) validate() error {
	switch c {
	case "", ContentCheckEnforce, ContentCheckReport:
		return nil
	default:
		return fmt.Errorf("slsa: content check must be %q or %q, got %q", ContentCheckEnforce, ContentCheckReport, string(c))
	}
}

// ContentStatus is the outcome of one subject's content check.
type ContentStatus string

const (
	// ContentVerified means the bytes are the file type the subject name declares.
	ContentVerified ContentStatus = "verified"
	// ContentUnchecked means provenance has no content rule for the subject name.
	ContentUnchecked ContentStatus = "unchecked"
	// ContentUnverified means the bytes are not the declared file type and ContentCheckReport
	// attested them anyway.
	ContentUnverified ContentStatus = "unverified"
)

// ContentVerdict reports whether one subject's bytes are the file type its name declares.
type ContentVerdict struct {
	// Name is the subject name the file type was read from.
	Name string
	// Format is the file type the name declares, as a noun phrase such as "a Debian binary
	// package (...)"; empty when no content rule covers the name.
	Format string
	// Status is the outcome of the check.
	Status ContentStatus
	// Reason says why the bytes are not Format; set only when Status is ContentUnverified.
	Reason string
}

// ErrContentMismatch marks a refusal because an artifact's bytes are not the file type its
// name declares.
var ErrContentMismatch = errors.New("content does not match the file type its name declares")

// contentProbe receives an artifact's bytes in file order while they are digested, and
// then judges them. Write never fails, so a malformed artifact never interrupts the digest.
type contentProbe interface {
	io.Writer
	// check returns why the size bytes the probe received are not its file type, or nil.
	check(size int64) error
}

// contentFormat is one file type provenance checks: its name for messages and the probe
// that judges an artifact's bytes.
type contentFormat struct {
	name  string
	probe func() contentProbe
}

var (
	debFormat = contentFormat{
		name:  "a Debian binary package (an ar archive of debian-binary, control.tar and data.tar)",
		probe: newDebProbe,
	}
	efiFormat = contentFormat{
		name:  "an EFI image (PE/COFF with an EFI subsystem)",
		probe: func() contentProbe { return newPEProbe(false) },
	}
	ukiFormat = contentFormat{
		name:  "a Unified Kernel Image (an EFI PE/COFF image with a .linux section)",
		probe: func() contentProbe { return newPEProbe(true) },
	}
)

// ukiNameWords are the file name words that present an .efi file as a kernel, and so as a
// Unified Kernel Image: an .efi kernel without the .linux section is a bare EFI-stub
// kernel, not a UKI.
var ukiNameWords = map[string]bool{"uki": true, "vmlinuz": true, "linux": true, "kernel": true}

// contentFormatFor returns the file type a subject name declares, and false when
// provenance has no content rule for the name. Only the extension and, for .efi, the
// words of the base name, an .addon.efi suffix and whether the file sits directly in the
// EFI/Linux directory are read; the comparison ignores case.
func contentFormatFor(name string) (contentFormat, bool) {
	slashed := strings.ToLower(strings.ReplaceAll(name, `\`, "/"))
	switch path.Ext(slashed) {
	case ".deb", ".udeb", ".ddeb":
		return debFormat, true
	case ".efi":
		if presentsUKI(slashed) {
			return ukiFormat, true
		}
		return efiFormat, true
	default:
		return contentFormat{}, false
	}
}

// ukiAddonSuffix ends the name of a systemd-stub addon, a PE image that carries .cmdline,
// .dtb, .initrd or .ucode sections for a UKI and never a .linux section. systemd-stub(7)
// loads addons from foo.efi.extra.d/ next to a UKI, inside EFI/Linux/, and from
// loader/addons/, so neither the directory nor a kernel word makes an addon a UKI.
const ukiAddonSuffix = ".addon.efi"

// presentsUKI reports whether a lower-case, slash-separated .efi subject name presents the
// file as a Unified Kernel Image: it is not an addon, and it sits directly in an EFI/Linux
// directory, where the Boot Loader Specification puts Type #2 images, or a word of its base
// name is one of ukiNameWords (vmlinuz-7.2.4.efi, arch-linux.efi, foo.uki.efi).
func presentsUKI(slashed string) bool {
	base := path.Base(slashed)
	if strings.HasSuffix(base, ukiAddonSuffix) {
		return false
	}
	if strings.HasSuffix(path.Dir("/"+slashed), "/efi/linux") {
		return true
	}
	stem := strings.TrimSuffix(base, ".efi")
	words := strings.FieldsFunc(stem, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, word := range words {
		if ukiNameWords[word] {
			return true
		}
	}
	return false
}

// contentProbeFor returns the probe for a subject name, or nil when no content rule
// covers it.
func contentProbeFor(name string) (contentFormat, contentProbe) {
	format, ok := contentFormatFor(name)
	if !ok {
		return contentFormat{}, nil
	}
	return format, format.probe()
}

// judgeContent turns a probe's finding into the subject's verdict, or into the refusal an
// enforced check makes of a mismatch. A nil probe means no content rule covers the name.
func judgeContent(req ProvenanceRequest, name string, format contentFormat, probe contentProbe, size int64) (ContentVerdict, error) {
	verdict := ContentVerdict{Name: name, Format: format.name, Status: ContentUnchecked}
	if probe == nil {
		return verdict, nil
	}
	mismatch := probe.check(size)
	switch {
	case mismatch == nil:
		verdict.Status = ContentVerified
		return verdict, nil
	case req.ContentCheck == ContentCheckReport:
		verdict.Status, verdict.Reason = ContentUnverified, mismatch.Error()
		return verdict, nil
	default:
		return ContentVerdict{}, fmt.Errorf("slsa: artifact %s: %w: expected %s, but %w",
			req.ArtifactPath, ErrContentMismatch, format.name, mismatch)
	}
}

// window collects the bytes of one stream range, [start, start+len(buf)), from the chunks
// that pass it in order.
type window struct {
	start  int64
	buf    []byte
	filled int
}

// collect copies from chunk, which begins at stream offset at, the bytes the window still
// lacks.
func (w *window) collect(chunk []byte, at int64) {
	next := w.start + int64(w.filled)
	if w.full() || next < at || next >= at+int64(len(chunk)) {
		return
	}
	w.filled += copy(w.buf[w.filled:], chunk[next-at:])
}

// full reports whether the window holds its whole range.
func (w *window) full() bool { return w.filled == len(w.buf) }

// bytes returns the part of the range collected so far.
func (w *window) bytes() []byte { return w.buf[:w.filled] }
