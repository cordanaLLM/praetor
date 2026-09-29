package supplychain

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// arMagic opens every ar archive, the container a Debian binary package is (deb(5)).
const arMagic = "!<arch>\n"

// arHeaderSize is the fixed size of one ar member header: name[16], mtime[12], uid[6],
// gid[6], mode[8], size[10] and the two-byte terminator.
const arHeaderSize = 60

// arHeaderTerminator ends every ar member header.
const arHeaderTerminator = "`\n"

// maxArMembers bounds the member headers one probe walks (HISS-02). A Debian package holds
// three, plus any underscore-prefixed members deb(5) reserves for later formats.
const maxArMembers = 16

// debPrefixBytes is how much of a package's start the probe keeps: the magic, the first
// member header and the debian-binary format version after it.
const debPrefixBytes = 128

// arMember is one ar archive member: its name, and where its data starts and how long it
// is.
type arMember struct {
	name   string
	offset int64
	size   int64
}

// debProbe walks the ar member headers of a Debian binary package as its bytes stream past,
// without holding the members' data.
type debProbe struct {
	seen      int64
	prefix    window
	header    window
	members   []arMember
	malformed error
}

func newDebProbe() contentProbe {
	return &debProbe{
		prefix: window{buf: make([]byte, debPrefixBytes)},
		header: window{start: int64(len(arMagic)), buf: make([]byte, arHeaderSize)},
	}
}

// Write collects the package's prefix and every member header the chunk holds.
func (d *debProbe) Write(chunk []byte) (int, error) {
	at := d.seen
	d.seen += int64(len(chunk))
	d.prefix.collect(chunk, at)
	for i := 0; i < maxArMembers && d.malformed == nil && len(d.members) < maxArMembers; i++ {
		d.header.collect(chunk, at)
		if !d.header.full() {
			break
		}
		d.addMember()
	}
	return len(chunk), nil
}

// addMember parses the collected header and moves the header window to the next member,
// which starts after the data and its padding to an even offset.
func (d *debProbe) addMember() {
	member, err := parseArHeader(d.header.buf, d.header.start)
	if err != nil {
		d.malformed = err
		return
	}
	d.members = append(d.members, member)
	d.header.start = member.offset + member.size + member.size%2
	d.header.filled = 0
}

// parseArHeader reads one full member header found at stream offset at.
func parseArHeader(header []byte, at int64) (arMember, error) {
	if string(header[58:arHeaderSize]) != arHeaderTerminator {
		return arMember{}, fmt.Errorf("the ar member header at offset %d lacks its terminator", at)
	}
	field := strings.TrimSpace(string(header[48:58]))
	size, err := strconv.ParseInt(field, 10, 64)
	if err != nil {
		return arMember{}, fmt.Errorf("the ar member header at offset %d declares size %q: %w", at, field, err)
	}
	if size < 0 || size > MaxArtifactBytes {
		return arMember{}, fmt.Errorf("the ar member header at offset %d declares %d bytes, outside 0 to %d", at, size, MaxArtifactBytes)
	}
	name := strings.TrimSuffix(strings.TrimRight(string(header[:16]), " "), "/")
	return arMember{name: name, offset: at + arHeaderSize, size: size}, nil
}

// check judges the archive, then the member order deb(5) prescribes.
func (d *debProbe) check(size int64) error {
	if err := d.checkArchive(size); err != nil {
		return err
	}
	return checkDebMembers(d.members, d.prefix.bytes())
}

// checkArchive refuses bytes that are not a complete ar archive: no magic, a malformed or
// cut-off member header, no member at all, or a member whose data runs past the end.
func (d *debProbe) checkArchive(size int64) error {
	switch {
	case !bytes.HasPrefix(d.prefix.bytes(), []byte(arMagic)):
		return fmt.Errorf("the file does not start with the ar archive magic %q", arMagic)
	case d.malformed != nil:
		return d.malformed
	case d.header.filled > 0:
		return fmt.Errorf("the file ends %d bytes into the ar member header at offset %d", d.header.filled, d.header.start)
	case len(d.members) == 0:
		return fmt.Errorf("the ar archive holds no members; the file is only the %d-byte magic", len(arMagic))
	}
	for _, member := range d.members {
		if member.offset+member.size > size {
			return fmt.Errorf("ar member %q declares %d bytes and the file ends %d bytes into it", member.name, member.size, size-member.offset)
		}
	}
	return nil
}

// checkDebMembers checks the order deb(5) prescribes: debian-binary holding a 2.x format
// version first, then control.tar and data.tar, each optionally preceded by members whose
// names start with an underscore. Members after data.tar are allowed.
func checkDebMembers(members []arMember, prefix []byte) error {
	first := members[0]
	if first.name != "debian-binary" {
		return fmt.Errorf("the first ar member is %q, not debian-binary", first.name)
	}
	if first.size < 2 || first.offset+2 > int64(len(prefix)) || !bytes.HasPrefix(prefix[first.offset:], []byte("2.")) {
		return fmt.Errorf("the debian-binary member does not hold a 2.x package format version")
	}
	rest, previous := members[1:], first.name
	for _, want := range []string{"control.tar", "data.tar"} {
		index := firstUnreserved(rest)
		if index == len(rest) {
			return fmt.Errorf("no %s member follows %s", want, previous)
		}
		if name := rest[index].name; name != want && !strings.HasPrefix(name, want+".") {
			return fmt.Errorf("ar member %q stands where %s belongs", name, want)
		}
		rest, previous = rest[index+1:], want
	}
	return nil
}

// firstUnreserved returns the index of the first member whose name does not start with the
// underscore deb(5) reserves for members older readers skip, or len(members).
func firstUnreserved(members []arMember) int {
	for i, member := range members {
		if !strings.HasPrefix(member.name, "_") {
			return i
		}
	}
	return len(members)
}
