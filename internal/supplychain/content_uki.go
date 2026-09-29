package supplychain

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ukiNameWord is the one base-name word that presents an .efi file as a Unified Kernel
// Image. Kernel words (vmlinuz, linux, kernel) do not: upstream Linux builds a bare EFI-stub
// kernel as vmlinuz.efi (KBUILD_IMAGE in arch/arm64/Makefile under CONFIG_EFI_ZBOOT), EFISTUB
// setups copy the kernel to names such as vmlinuz-linux.efi, and neither carries a .linux
// section. Such a kernel only has to be an EFI image; a UKI under a kernel name is declared
// with ProvenanceRequest.UKIGlobs instead (#616).
const ukiNameWord = "uki"

// ukiAddonSuffix ends the name of a systemd-stub addon, a PE image that carries .cmdline,
// .dtb, .initrd or .ucode sections for a UKI and never a .linux section. systemd-stub(7)
// loads addons from foo.efi.extra.d/ next to a UKI and from loader/addons/, so the uki
// word never makes an addon a UKI.
const ukiAddonSuffix = ".addon.efi"

// presentsUKI reports whether a lower-case, slash-separated .efi subject name presents the
// file as a Unified Kernel Image: it is not an addon, and it sits directly in an EFI/Linux
// directory, where the Boot Loader Specification puts Type #2 images, or a word of its base
// name is ukiNameWord (image.uki.efi, arch-linux-uki.efi). A word is a run of letters and
// digits, so ukify.efi and vmlinuz-7.2.4.efi are not presented as UKIs.
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
		if word == ukiNameWord {
			return true
		}
	}
	return false
}

// maxUKIGlobs bounds the UKI globs one provenance run declares, and maxUKIGlobSegments the
// slash-separated segments of one glob (HISS-02).
const (
	maxUKIGlobs        = 64
	maxUKIGlobSegments = 64
)

// validateUKIGlobs refuses more than maxUKIGlobs globs and any glob validateUKIGlob refuses,
// before any file is read.
func validateUKIGlobs(globs []string) error {
	if len(globs) > maxUKIGlobs {
		return fmt.Errorf("slsa: %d UKI globs exceed the limit of %d", len(globs), maxUKIGlobs)
	}
	for i := 0; i < len(globs) && i < maxUKIGlobs; i++ {
		if err := validateUKIGlob(globs[i]); err != nil {
			return err
		}
	}
	return nil
}

// validateUKIGlob refuses a blank glob, one of more than maxUKIGlobSegments segments, and a
// segment path.Match cannot parse, which would otherwise match nothing without a word.
func validateUKIGlob(glob string) error {
	if strings.TrimSpace(glob) == "" {
		return fmt.Errorf("slsa: a UKI glob cannot be blank")
	}
	segments := strings.Split(glob, "/")
	if len(segments) > maxUKIGlobSegments {
		return fmt.Errorf("slsa: UKI glob %q has %d segments, more than the limit of %d", glob, len(segments), maxUKIGlobSegments)
	}
	for i := 0; i < len(segments) && i < maxUKIGlobSegments; i++ {
		if _, err := path.Match(segments[i], ""); err != nil {
			return fmt.Errorf("slsa: UKI glob %q: %w", glob, err)
		}
	}
	return nil
}

// matchingUKIGlob returns the first glob that matches a subject name, or "" when none does.
// The name is compared in slash form and in full, case included: each glob segment is a
// path.Match pattern for one name segment, and a "**" segment spans any number of them
// (util.MatchGlobSegments), so "*.efi" matches vmlinuz.efi but not EFI/Linux/entry.efi,
// which "**/*.efi" matches.
func matchingUKIGlob(globs []string, name string) string {
	segments := strings.Split(util.NormalizeSlashes(name), "/")
	for i := 0; i < len(globs) && i < maxUKIGlobs; i++ {
		if util.MatchGlobSegments(strings.Split(globs[i], "/"), segments) {
			return globs[i]
		}
	}
	return ""
}

// checkUKIGlobsMatched refuses a UKI glob that matches none of the subjects: a declared
// Unified Kernel Image that no subject answers to would otherwise go unchecked without a
// word, as a mistyped glob does.
func checkUKIGlobsMatched(globs []string, subjects []Subject) error {
	for i := 0; i < len(globs) && i < maxUKIGlobs; i++ {
		matched := false
		for j := 0; j < len(subjects) && j < maxProvenanceSubjects && !matched; j++ {
			matched = matchingUKIGlob(globs[i:i+1], subjects[j].Name) != ""
		}
		if !matched {
			return fmt.Errorf("slsa: UKI glob %q matches no subject name; a Unified Kernel Image it declares would go unchecked", globs[i])
		}
	}
	return nil
}
