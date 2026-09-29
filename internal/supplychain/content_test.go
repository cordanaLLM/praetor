package supplychain

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// arMemberFixture is one member of a test ar archive.
type arMemberFixture struct {
	name string
	data []byte
}

// arArchive builds an ar archive of members. With slash, each name carries the trailing
// slash GNU ar writes; without it, names are space-padded as dpkg-deb writes them.
func arArchive(slash bool, members ...arMemberFixture) []byte {
	var b bytes.Buffer
	b.WriteString(arMagic)
	for _, member := range members {
		name := member.name
		if slash {
			name += "/"
		}
		fmt.Fprintf(&b, "%-16s%-12d%-6d%-6d%-8s%-10d%s", name, 0, 0, 0, "100644", len(member.data), arHeaderTerminator)
		b.Write(member.data)
		if len(member.data)%2 == 1 {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// debMembers are the three members of a minimal Debian binary package. The control member
// has an odd length, so the archive carries the padding byte ar requires after it.
func debMembers() []arMemberFixture {
	return []arMemberFixture{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar.gz", bytes.Repeat([]byte{0x1f}, 33)},
		{"data.tar.xz", bytes.Repeat([]byte{0xfd}, 96)},
	}
}

// peSectionFixture is one section of a test PE image.
type peSectionFixture struct {
	name string
	data []byte
}

// peFixtureHeaderOffset is where the fixture's PE signature starts.
const peFixtureHeaderOffset = 0x80

// peFixture builds a PE32+ image with an EFI subsystem value, a 240-byte optional header,
// and each section's raw data after the section table, as ukify and the kernel's EFI stub
// lay theirs out.
func peFixture(subsystem uint16, sections ...peSectionFixture) []byte {
	const optionalSize = 240
	coff := peFixtureHeaderOffset + peSignatureSize
	optional := coff + peCOFFHeaderSize
	table := optional + optionalSize
	image := make([]byte, table+peSectionHeaderSize*len(sections))
	copy(image, peDOSMagic)
	binary.LittleEndian.PutUint32(image[peNewHeaderOffsetAt:], peFixtureHeaderOffset)
	copy(image[peFixtureHeaderOffset:], peSignature)
	binary.LittleEndian.PutUint16(image[coff:], 0x8664)
	binary.LittleEndian.PutUint16(image[coff+2:], uint16(len(sections)))
	binary.LittleEndian.PutUint16(image[coff+16:], optionalSize)
	binary.LittleEndian.PutUint16(image[optional:], pe32PlusMagic)
	binary.LittleEndian.PutUint16(image[optional+peSubsystemAt:], subsystem)
	raw := len(image)
	for i, section := range sections {
		entry := table + i*peSectionHeaderSize
		copy(image[entry:entry+peSectionNameBytes], section.name)
		binary.LittleEndian.PutUint32(image[entry+8:], uint32(len(section.data)))
		binary.LittleEndian.PutUint32(image[entry+peSectionRawSizeAt:], uint32(len(section.data)))
		binary.LittleEndian.PutUint32(image[entry+peSectionRawOffsetAt:], uint32(raw))
		raw += len(section.data)
	}
	for _, section := range sections {
		image = append(image, section.data...)
	}
	return image
}

// ukiFixture lays out the sections ukify writes: the stub's own, then .osrel, .cmdline,
// .uname and the kernel in .linux.
func ukiFixture() []byte {
	return peFixture(peEFIApplication,
		peSectionFixture{".text", bytes.Repeat([]byte{0xcc}, 64)},
		peSectionFixture{".sbat", []byte("sbat,1\n")},
		peSectionFixture{".osrel", []byte("ID=example\n")},
		peSectionFixture{".cmdline", []byte("console=ttyS0\n")},
		peSectionFixture{".uname", []byte("7.2.4\n")},
		peSectionFixture{".linux", bytes.Repeat([]byte{0x90}, 512)},
	)
}

// addonFixture lays out the sections ukify writes for a systemd-stub addon built with
// --stub=addonx64.efi.stub --cmdline=quiet: no .linux, which addons never carry.
func addonFixture() []byte {
	return peFixture(peEFIApplication,
		peSectionFixture{".text", bytes.Repeat([]byte{0xcc}, 11)},
		peSectionFixture{".sdmagic", []byte("#### LoaderInfo: systemd-addon ####")},
		peSectionFixture{".cmdline", []byte("quiet")},
		peSectionFixture{".sbat", []byte("sbat,1\n")},
	)
}

// bareKernelFixture lays out the sections of an EFI-stub kernel, which has no .linux.
func bareKernelFixture() []byte {
	return peFixture(peEFIApplication,
		peSectionFixture{".setup", bytes.Repeat([]byte{1}, 32)},
		peSectionFixture{".compat", bytes.Repeat([]byte{2}, 16)},
		peSectionFixture{".text", bytes.Repeat([]byte{3}, 256)},
		peSectionFixture{".data", bytes.Repeat([]byte{4}, 32)},
	)
}

// probeInChunks feeds content to a fresh probe for name in chunks of size bytes, as the
// digest stream does, and returns the probe's finding.
func probeInChunks(t *testing.T, name string, content []byte, size int) error {
	t.Helper()
	_, probe := contentProbeFor(name, false)
	if probe == nil {
		t.Fatalf("no content rule for %s", name)
	}
	for start := 0; start < len(content); start += size {
		end := min(start+size, len(content))
		if n, err := probe.Write(content[start:end]); err != nil || n != end-start {
			t.Fatalf("probe write %d..%d = %d, %v", start, end, n, err)
		}
	}
	return probe.check(int64(len(content)))
}

// probeAllChunkings runs probeInChunks with chunk sizes that split every header at a
// different place and fails unless all of them reach the same finding.
func probeAllChunkings(t *testing.T, name string, content []byte) error {
	t.Helper()
	want := probeInChunks(t, name, content, len(content)+1)
	for _, size := range []int{1, 7, 59, 61, 4096} {
		got := probeInChunks(t, name, content, size)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s in %d-byte chunks: %v, whole: %v", name, size, got, want)
		}
	}
	return want
}

func TestParseContentCheck(t *testing.T) {
	for value, want := range map[string]ContentCheck{"": ContentCheckEnforce, "enforce": ContentCheckEnforce, "report": ContentCheckReport} {
		if got, err := ParseContentCheck(value); err != nil || got != want {
			t.Errorf("ParseContentCheck(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	for _, value := range []string{"off", "Enforce", " report", "warn"} {
		if _, err := ParseContentCheck(value); err == nil {
			t.Errorf("ParseContentCheck(%q) accepted", value)
		}
	}
}

func TestContentFormatFor_NamesSelectRules(t *testing.T) {
	cases := map[string]string{
		"linux-image-7.2.4_amd64.deb": debFormat.name,
		"PKG.DEB":                     debFormat.name,
		"installer.udeb":              debFormat.name,
		"tool-dbgsym_1.0_amd64.ddeb":  debFormat.name,
		"BOOTX64.EFI":                 efiFormat.name,
		"systemd-bootx64.efi":         efiFormat.name,
		"linuxx64.efi":                efiFormat.name,
		"vmlinuz.efi":                 efiFormat.name,
		"vmlinuz-7.2.4.efi":           efiFormat.name,
		"vmlinuz-linux.efi":           efiFormat.name,
		"arch-linux.efi":              efiFormat.name,
		"Kernel.EFI":                  efiFormat.name,
		"ukify.efi":                   efiFormat.name,
		"image.uki.efi":               ukiFormat.name,
		"arch-linux-UKI.efi":          ukiFormat.name,
		"uki.efi":                     ukiFormat.name,
		"EFI/Linux/example-7.2.4.efi": ukiFormat.name,
		`EFI\Linux\example.efi`:       ukiFormat.name,
		"boot/EFI/Linux/example.efi":  ukiFormat.name,
		"EFI/Linux/nested/entry.efi":  efiFormat.name,
		"notEFI/Linux/entry.efi":      efiFormat.name,
		"EFI/Linux/test.efi.extra.d/quiet.addon.efi": efiFormat.name,
		"EFI/Linux/direct.addon.efi":                 efiFormat.name,
		"loader/addons/kernel-cmdline.addon.efi":     efiFormat.name,
		"uki-cmdline.addon.efi":                      efiFormat.name,
		"Linux-Debug.Addon.EFI":                      efiFormat.name,
		"vmlinuz.addon.efi.sig":                      "",
		"praetor_1.0.0_linux_amd64.tar.gz":           "",
		"package.deb.sig":                            "",
		"deb":                                        "",
		"efi":                                        "",
	}
	for name, want := range cases {
		format, ok := contentFormatFor(name, false)
		if format.name != want || ok != (want != "") {
			t.Errorf("contentFormatFor(%q) = %q, %v; want %q", name, format.name, ok, want)
		}
	}
	// A declared UKI gets the UKI rule whatever its name, extension and addon suffix.
	for _, name := range []string{"vmlinuz-7.2.4.efi", "BOOTX64.EFI", "kernel.img", "quiet.addon.efi"} {
		if format, ok := contentFormatFor(name, true); !ok || format.name != ukiFormat.name {
			t.Errorf("contentFormatFor(%q, declared) = %q, %v; want the UKI rule", name, format.name, ok)
		}
	}
}

func TestDebProbe_Positive_PackageLayouts(t *testing.T) {
	members := debMembers()
	reserved := arMemberFixture{"_gpgbuilder", []byte("signature")}
	layouts := map[string][]byte{
		"dpkg-deb names":                 arArchive(false, members...),
		"GNU ar names":                   arArchive(true, members...),
		"reserved member before control": arArchive(false, members[0], reserved, members[1], members[2]),
		"reserved member after data":     arArchive(false, append(debMembers(), reserved)...),
		"uncompressed members":           arArchive(false, members[0], arMemberFixture{"control.tar", []byte("c")}, arMemberFixture{"data.tar", []byte("d")}),
	}
	for layout, content := range layouts {
		if err := probeAllChunkings(t, "pkg.deb", content); err != nil {
			t.Errorf("%s refused: %v", layout, err)
		}
	}
}

func TestDebProbe_Negative_NotADebianPackage(t *testing.T) {
	members := debMembers()
	badTerminator := arArchive(false, members...)
	copy(badTerminator[len(arMagic)+58:], "xx")
	badSize := arArchive(false, members...)
	copy(badSize[len(arMagic)+48:], "4x        ")
	cases := map[string]struct {
		content []byte
		want    string
	}{
		"text file":         {[]byte("not a debian package\n"), "ar archive magic"},
		"plain ar archive":  {arArchive(false, arMemberFixture{"hello.o", []byte("obj")}), "not debian-binary"},
		"format version 3":  {arArchive(false, arMemberFixture{"debian-binary", []byte("3.0\n")}, members[1], members[2]), "2.x package format"},
		"no data member":    {arArchive(false, members[0], members[1]), "no data.tar member"},
		"no control member": {arArchive(false, members[0], reservedOnly()), "no control.tar member"},
		"swapped members":   {arArchive(false, members[0], members[2], members[1]), `"data.tar.xz" stands where control.tar belongs`},
		"lookalike name":    {arArchive(false, members[0], arMemberFixture{"control.tarball", []byte("x")}, members[2]), "stands where control.tar belongs"},
		"broken terminator": {badTerminator, "lacks its terminator"},
		"non-decimal size":  {badSize, "declares size"},
		"oversized member":  {oversizedMember(), "outside 0 to"},
	}
	for name, tc := range cases {
		err := probeAllChunkings(t, "pkg.deb", tc.content)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// reservedOnly is a reserved member, which never stands in for control.tar.
func reservedOnly() arMemberFixture { return arMemberFixture{"_extra", []byte("x")} }

// oversizedMember is an archive whose first header declares more bytes than any artifact
// provenance attests, so its end offset cannot overflow the size comparison.
func oversizedMember() []byte {
	content := arArchive(false, arMemberFixture{"debian-binary", []byte("2.0\n")})
	copy(content[len(arMagic)+48:], "9999999999")
	return content
}

func TestDebProbe_Boundary_TruncatedArchives(t *testing.T) {
	whole := arArchive(false, debMembers()...)
	cases := map[string]struct {
		content []byte
		want    string
	}{
		"one byte":                  {[]byte("!"), "ar archive magic"},
		"magic alone":               {[]byte(arMagic), "holds no members"},
		"cut inside a header":       {whole[:len(arMagic)+30], "30 bytes into the ar member header at offset 8"},
		"cut inside member data":    {whole[:len(whole)-10], `ar member "data.tar.xz" declares 96 bytes and the file ends 86 bytes into it`},
		"stray bytes after members": {append(append([]byte{}, whole...), "tail"...), "4 bytes into the ar member header"},
	}
	for name, tc := range cases {
		err := probeAllChunkings(t, "pkg.deb", tc.content)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
	if err := probeAllChunkings(t, "pkg.deb", whole); err != nil {
		t.Errorf("the untruncated package is refused: %v", err)
	}
}

func TestPEProbe_Positive_EFIImagesAndUKI(t *testing.T) {
	efi := peFixture(peEFIApplication, peSectionFixture{".text", bytes.Repeat([]byte{0xcc}, 128)})
	for name, content := range map[string][]byte{
		"BOOTX64.EFI":         efi,
		"driver.efi":          peFixture(11, peSectionFixture{".text", []byte{1}}),
		"option-rom.efi":      peFixture(peEFIROM, peSectionFixture{".text", []byte{1}}),
		"stub.efi":            bareKernelFixture(),
		"vmlinuz-7.efi":       bareKernelFixture(),
		"vmlinuz-linux.efi":   bareKernelFixture(),
		"vmlinuz-7.2.4.efi":   ukiFixture(),
		"image.uki.efi":       ukiFixture(),
		"EFI/Linux/entry.efi": ukiFixture(),
		"EFI/Linux/test.efi.extra.d/quiet.addon.efi": addonFixture(),
		"loader/addons/kernel-cmdline.addon.efi":     addonFixture(),
	} {
		if err := probeAllChunkings(t, name, content); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	pe32 := peFixture(peEFIApplication, peSectionFixture{".text", []byte{1}})
	binary.LittleEndian.PutUint16(pe32[peFixtureHeaderOffset+peSignatureSize+peCOFFHeaderSize:], pe32Magic)
	if err := probeAllChunkings(t, "ia32.efi", pe32); err != nil {
		t.Errorf("a PE32 EFI image is refused: %v", err)
	}
}

func TestPEProbe_Negative_NotAnEFIImage(t *testing.T) {
	random := bytes.Repeat([]byte{0x5a, 0x4d, 0x00, 0x13}, 4096)
	badSignature := ukiFixture()
	copy(badSignature[peFixtureHeaderOffset:], "NE\x00\x00")
	badMagic := ukiFixture()
	binary.LittleEndian.PutUint16(badMagic[peFixtureHeaderOffset+peSignatureSize+peCOFFHeaderSize:], 0x107)
	shortOptional := ukiFixture()
	binary.LittleEndian.PutUint16(shortOptional[peFixtureHeaderOffset+peSignatureSize+16:], peOptionalHeaderMin-1)
	cases := map[string]struct {
		name    string
		content []byte
		want    string
	}{
		"random bytes":             {"vmlinuz-7.2.4.efi", random, "MZ"},
		"text placeholder":         {"vmlinuz-7.2.4.efi", []byte(strings.Repeat("placeholder kernel image\n", 4)), "MZ"},
		"bare kernel in EFI/Linux": {"EFI/Linux/x.efi", bareKernelFixture(), "no .linux section"},
		"bare kernel, uki word":    {"vmlinuz-7.2.4-uki.efi", bareKernelFixture(), "no .linux section"},
		"empty .linux":             {"image.uki.efi", peFixture(peEFIApplication, peSectionFixture{".linux", nil}), "no .linux section"},
		"Windows console app":      {"BOOTX64.EFI", peFixture(3, peSectionFixture{".text", []byte{1}}), "subsystem is 3"},
		"subsystem below EFI":      {"BOOTX64.EFI", peFixture(peEFIApplication-1, peSectionFixture{".text", []byte{1}}), "subsystem is 9"},
		"subsystem above EFI":      {"BOOTX64.EFI", peFixture(peEFIROM+1, peSectionFixture{".text", []byte{1}}), "subsystem is 14"},
		"no PE signature":          {"BOOTX64.EFI", badSignature, "no PE signature"},
		"unknown magic":            {"BOOTX64.EFI", badMagic, "neither PE32"},
		"short optional header":    {"BOOTX64.EFI", shortOptional, "too short to name a subsystem"},
		"no sections":              {"BOOTX64.EFI", peFixture(peEFIApplication), "declares no sections"},
	}
	for label, tc := range cases {
		err := probeAllChunkings(t, tc.name, tc.content)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", label, err, tc.want)
		}
	}
}

func TestPEProbe_Boundary_TruncatedAndDistantHeaders(t *testing.T) {
	uki := ukiFixture()
	if err := probeAllChunkings(t, "image.uki.efi", uki); err != nil {
		t.Fatalf("a UKI whose .linux section ends exactly at the end of the file is refused: %v", err)
	}
	distant := append(ukiFixture(), make([]byte, peHeaderWindow)...)
	binary.LittleEndian.PutUint32(distant[peNewHeaderOffsetAt:], peHeaderWindow)
	cases := map[string]struct {
		content []byte
		want    string
	}{
		"MZ and four bytes":       {[]byte("MZfake"), "ends at byte 6, before the end of the DOS header"},
		"DOS header alone":        {uki[:peDOSHeaderSize], "before the end of the PE signature and COFF header"},
		"cut in section table":    {uki[:peFixtureHeaderOffset+peSignatureSize+peCOFFHeaderSize+240+10], "before the end of the PE section table"},
		"last byte of .linux cut": {uki[:len(uki)-1], `PE section ".linux" ends at byte`},
		"headers past the window": {distant, "lies past the first 65536 bytes"},
	}
	for label, tc := range cases {
		err := probeAllChunkings(t, "image.uki.efi", tc.content)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", label, err, tc.want)
		}
	}
}

func TestGenerateSLSAProvenance_ContentVerdicts(t *testing.T) {
	deb, _ := writeArtifact(t, "tool_1.0_amd64.deb", arArchive(false, debMembers()...))
	uki, _ := writeArtifact(t, "image.uki.efi", ukiFixture())
	other, _ := writeArtifact(t, "tool.tar.gz", []byte("archive bytes"))
	want := map[string]ContentStatus{deb: ContentVerified, uki: ContentVerified, other: ContentUnchecked}
	for path, status := range want {
		stmt, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b"})
		if err != nil || stmt == nil || len(verdicts) != 1 || verdicts[0].Status != status || verdicts[0].Reason != "" {
			t.Errorf("%s: verdicts %+v, err %v; want %s", path, verdicts, err, status)
		}
	}
}

func TestGenerateSLSAProvenance_ContentMismatchEnforcedOrReported(t *testing.T) {
	path, digest := writeArtifact(t, "linux-image-7.2.4_amd64.deb", []byte("not a debian package\n"))
	for _, mode := range []ContentCheck{"", ContentCheckEnforce} {
		stmt, _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b", ContentCheck: mode})
		if stmt != nil || !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), "linux-image-7.2.4_amd64.deb") || !strings.Contains(err.Error(), "Debian binary package") {
			t.Errorf("mode %q: statement %v, err %v; want a refusal naming the file and the expected format", mode, stmt, err)
		}
	}
	stmt, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b", ContentCheck: ContentCheckReport})
	if err != nil || stmt.Subject[0].Digest["sha256"] != digest {
		t.Fatalf("report mode refused or changed the digest: %v", err)
	}
	if verdicts[0].Status != ContentUnverified || verdicts[0].Format != debFormat.name || !strings.Contains(verdicts[0].Reason, "ar archive magic") {
		t.Errorf("report mode verdict = %+v", verdicts[0])
	}
	if _, _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: path, BuilderID: "b", ContentCheck: "off"}); err == nil || !strings.Contains(err.Error(), "content check must be") {
		t.Errorf("unknown content check mode accepted: %v", err)
	}
}

func TestGenerateSLSAProvenanceFromChecksums_ContentCheck(t *testing.T) {
	manifest, _ := releaseManifest(t, []string{"a.tar.gz", "BOOTX64.EFI"})
	stmt, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b"})
	if stmt != nil || !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), "checksum line 2") || !strings.Contains(err.Error(), "EFI image") {
		t.Fatalf("a text BOOTX64.EFI in a manifest was not refused by line: %v", err)
	}
	stmt, verdicts, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b", ContentCheck: ContentCheckReport})
	if err != nil || len(stmt.Subject) != 2 || len(verdicts) != 2 {
		t.Fatalf("report mode: %v, %+v", err, verdicts)
	}
	if verdicts[0].Name != "a.tar.gz" || verdicts[0].Status != ContentUnchecked || verdicts[1].Name != "BOOTX64.EFI" || verdicts[1].Status != ContentUnverified {
		t.Errorf("verdicts out of manifest order or wrong: %+v", verdicts)
	}
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b", ContentCheck: "skip"}); err == nil || !strings.Contains(err.Error(), "content check must be") {
		t.Errorf("unknown content check mode accepted: %v", err)
	}
}
