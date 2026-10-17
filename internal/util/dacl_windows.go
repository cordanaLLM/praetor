// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package util

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Windows protects a file with its DACL, which os.Chmod does not touch: a file written 0600
// inherits whatever its directory grants (#101). The calls below set and read that DACL
// through advapi32 directly; the module carries no golang.org/x/sys dependency.
var (
	advapi32                        = syscall.NewLazyDLL("advapi32.dll")
	kernel32                        = syscall.NewLazyDLL("kernel32.dll")
	procConvertStringSDToSD         = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procConvertSDToStringSD         = advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
	procGetSecurityDescriptorDacl   = advapi32.NewProc("GetSecurityDescriptorDacl")
	procSetSecurityInfo             = advapi32.NewProc("SetSecurityInfo")
	procSetNamedSecurityInfo        = advapi32.NewProc("SetNamedSecurityInfoW")
	procGetNamedSecurityInfo        = advapi32.NewProc("GetNamedSecurityInfoW")
	procReOpenFile                  = kernel32.NewProc("ReOpenFile")
	errUnexpectedSecurityDescriptor = fmt.Errorf("util: security descriptor carries no DACL")
)

const (
	seFileObject                     = 1
	daclSecurityInformation          = 0x00000004
	protectedDaclSecurityInformation = 0x80000000
	sddlRevision1                    = 1
	accessWriteDAC                   = 0x00040000
	accessReadControl                = 0x00020000
	fileShareAll                     = syscall.FILE_SHARE_READ | syscall.FILE_SHARE_WRITE | syscall.FILE_SHARE_DELETE
	// maxDACLEntries bounds the walk over one DACL's entries (HISS-02).
	maxDACLEntries = 256
)

// currentUserSID returns the string SID of the account this process runs as.
func currentUserSID() (string, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("util: open process token: %w", err)
	}
	defer token.Close() //nolint:errcheck // read-only token handle
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("util: read token user: %w", err)
	}
	return user.User.Sid.String()
}

// ownerOnlySDDL is the DACL of a private artefact: full access for this account, SYSTEM and
// the Administrators group, nothing inherited from the directory (the P flag). A directory's
// entries are inherited by what is created in it (OICI).
func ownerOnlySDDL(dir bool) (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	inherit := ""
	if dir {
		inherit = "OICI"
	}
	return fmt.Sprintf("D:P(A;%[1]s;FA;;;%[2]s)(A;%[1]s;FA;;;SY)(A;%[1]s;FA;;;BA)", inherit, sid), nil
}

// withDACL converts sddl to a security descriptor and calls fn with its DACL.
func withDACL(sddl string, fn func(dacl uintptr) error) error {
	text, err := syscall.UTF16PtrFromString(sddl)
	if err != nil {
		return err
	}
	var sd uintptr
	// SAFETY: text is the NUL-terminated UTF-16 string UTF16PtrFromString returned and sd a
	// local the call writes the descriptor address into; both are converted to uintptr inside
	// the call expression, so they stay valid for the whole system call (unsafe.Pointer rule 4).
	if ok, _, callErr := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(text)), sddlRevision1,
		uintptr(unsafe.Pointer(&sd)), 0); ok == 0 {
		return fmt.Errorf("util: convert security descriptor %q: %w", sddl, callErr)
	}
	defer syscall.LocalFree(syscall.Handle(sd)) //nolint:errcheck // frees the converted descriptor
	var present, defaulted int32
	var dacl uintptr
	// SAFETY: present, dacl and defaulted are locals that outlive the call, converted inside the
	// call expression (unsafe.Pointer rule 4); sd is the descriptor converted above, freed only
	// by the deferred LocalFree after fn has used the DACL that points into it.
	if ok, _, callErr := procGetSecurityDescriptorDacl.Call(sd, uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); ok == 0 {
		return fmt.Errorf("util: read DACL of %q: %w", sddl, callErr)
	}
	if present == 0 {
		return errUnexpectedSecurityDescriptor
	}
	return fn(dacl)
}

// setNamedDACL replaces the DACL of the file or directory at path with sddl's, cutting
// inheritance from the parent when sddl is protected.
func setNamedDACL(path, sddl string) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	info := uintptr(daclSecurityInformation)
	if strings.HasPrefix(sddl, "D:P") {
		info |= protectedDaclSecurityInformation
	}
	return withDACL(sddl, func(dacl uintptr) error {
		// SAFETY: name is the NUL-terminated UTF-16 path UTF16PtrFromString returned, kept alive
		// by the closure and converted inside the call expression (unsafe.Pointer rule 4).
		if code, _, _ := procSetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject, info, //nolint:errcheck // the API returns its error as the result code, checked below
			0, 0, dacl, 0); code != 0 {
			return fmt.Errorf("util: set DACL of %q: %w", path, syscall.Errno(code))
		}
		return nil
	})
}

// restrictToOwner gives the open file or directory the owner-only DACL through a second
// handle with WRITE_DAC. A file is reopened through its own handle (ReOpenFile), so no path is
// resolved again. ReOpenFile refuses directories, so a directory is opened by name without
// following a reparse point, and that handle must name the same directory as file: a name
// swapped since the open is refused instead of changed.
func restrictToOwner(file *os.File, dir bool) error {
	sddl, err := ownerOnlySDDL(dir)
	if err != nil {
		return err
	}
	handle, err := writeDACHandle(file, dir)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle) //nolint:errcheck // handle only carries WRITE_DAC
	return withDACL(sddl, func(dacl uintptr) error {
		if code, _, _ := procSetSecurityInfo.Call(uintptr(handle), seFileObject, //nolint:errcheck // the API returns its error as the result code, checked below
			daclSecurityInformation|protectedDaclSecurityInformation, 0, 0, dacl, 0); code != 0 {
			return fmt.Errorf("util: set owner-only DACL of %q: %w", file.Name(), syscall.Errno(code))
		}
		return nil
	})
}

// writeDACHandle returns a handle on the object file names that may change its DACL. A
// directory is opened by its name, following a link the way os.Open did, so an in-root link
// gets the DACL on the directory it names; the identity check refuses a name that now names
// another object than the one file holds.
func writeDACHandle(file *os.File, dir bool) (syscall.Handle, error) {
	if !dir {
		handle, _, callErr := procReOpenFile.Call(file.Fd(), accessWriteDAC|accessReadControl, fileShareAll, 0)
		if syscall.Handle(handle) == syscall.InvalidHandle {
			return 0, fmt.Errorf("util: reopen %q to set its DACL: %w", file.Name(), callErr)
		}
		return syscall.Handle(handle), nil
	}
	name, err := syscall.UTF16PtrFromString(file.Name())
	if err != nil {
		return 0, err
	}
	handle, err := syscall.CreateFile(name, accessWriteDAC|accessReadControl, fileShareAll, nil, syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, fmt.Errorf("util: open %q to set its DACL: %w", file.Name(), err)
	}
	var opened, named syscall.ByHandleFileInformation
	if err := errors.Join(syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &opened),
		syscall.GetFileInformationByHandle(handle, &named)); err != nil {
		syscall.CloseHandle(handle) //nolint:errcheck // closing after a failed identity read
		return 0, fmt.Errorf("util: identify %q to set its DACL: %w", file.Name(), err)
	}
	if opened.VolumeSerialNumber != named.VolumeSerialNumber || opened.FileIndexHigh != named.FileIndexHigh ||
		opened.FileIndexLow != named.FileIndexLow {
		syscall.CloseHandle(handle) //nolint:errcheck // closing a handle on the wrong object
		return 0, fmt.Errorf("util: %q changed before its DACL could be set", file.Name())
	}
	return handle, nil
}

// restrictFileAccess applies the owner-only DACL to the open file or directory when perm
// grants no group or other access, the Windows form of what the mode means on POSIX. A wider
// perm leaves the inherited DACL.
func restrictFileAccess(file *os.File, perm os.FileMode) error {
	if perm.Perm()&0o077 != 0 {
		return nil
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("util: stat %q: %w", file.Name(), err)
	}
	return restrictToOwner(file, info.IsDir())
}

// namedDACLString returns the DACL of path in SDDL form.
func namedDACLString(path string) (string, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var dacl, sd uintptr
	// SAFETY: name is the NUL-terminated UTF-16 path UTF16PtrFromString returned; dacl and sd are
	// locals the call writes, all converted inside the call expression (unsafe.Pointer rule 4).
	if code, _, _ := procGetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject, //nolint:errcheck // the API returns its error as the result code, checked below
		daclSecurityInformation, 0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd))); code != 0 {
		return "", fmt.Errorf("util: read DACL of %q: %w", path, syscall.Errno(code))
	}
	defer syscall.LocalFree(syscall.Handle(sd)) //nolint:errcheck // frees the returned descriptor
	return formatDACL(sd, path)
}

// formatDACL returns the DACL of the security descriptor sd in SDDL form, the way the system
// writes it: a well-known account appears under its alias. what names sd in an error.
func formatDACL(sd uintptr, what string) (string, error) {
	var text *uint16
	// SAFETY: text is a local the call writes the address of a LocalAlloc'ed string into,
	// converted inside the call expression (unsafe.Pointer rule 4).
	if ok, _, callErr := procConvertSDToStringSD.Call(sd, sddlRevision1, daclSecurityInformation,
		uintptr(unsafe.Pointer(&text)), 0); ok == 0 {
		return "", fmt.Errorf("util: format DACL of %q: %w", what, callErr)
	}
	// SAFETY: text was allocated by the system with LocalAlloc, which LocalFree takes back by its
	// address as a handle; it is freed once, after the last read below.
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(text))) //nolint:errcheck // frees the string
	// SAFETY: maxSDDLChars returns the index of the string's NUL terminator, which the system
	// writes inside the allocation, so the slice covers only characters before it.
	return syscall.UTF16ToString(unsafe.Slice(text, maxSDDLChars(text))), nil
}

// maxSDDLChars returns the length of the NUL-terminated string at text, at most 64 Ki
// characters (HISS-02).
func maxSDDLChars(text *uint16) int {
	const limit = 1 << 16
	// SAFETY: the system NUL-terminates the string inside its allocation, and the loop below
	// reads no element past the first NUL, so no character beyond the allocation is read.
	chars := unsafe.Slice(text, limit)
	for i, c := range chars {
		if c == 0 {
			return i
		}
	}
	return limit
}

// OwnerOnlyAccess reports whether the DACL of path grants access to no one but this account,
// SYSTEM and the Administrators group; otherwise it names the first other trustee. A missing
// (null) DACL grants everyone access.
func OwnerOnlyAccess(path string) (bool, string, error) {
	sddl, err := namedDACLString(path)
	if err != nil {
		return false, "", err
	}
	sid, err := currentUserSID()
	if err != nil {
		return false, "", err
	}
	alias, err := userTrustee(sid)
	if err != nil {
		return false, "", err
	}
	return ownerOnlyEntries(sddl, sid, alias)
}

// userTrustee returns the name the system writes sid under in a DACL: its alias for a well-known
// account, such as LA for the built-in Administrator a CI runner may run as, else sid itself.
// It formats a one-entry DACL naming sid and reads the entry's trustee back.
func userTrustee(sid string) (string, error) {
	text, err := syscall.UTF16PtrFromString("D:(A;;FA;;;" + sid + ")")
	if err != nil {
		return "", err
	}
	var sd uintptr
	// SAFETY: text is the NUL-terminated UTF-16 string UTF16PtrFromString returned and sd a
	// local the call writes the descriptor address into; both are converted to uintptr inside
	// the call expression, so they stay valid for the whole system call (unsafe.Pointer rule 4).
	if ok, _, callErr := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(text)), sddlRevision1,
		uintptr(unsafe.Pointer(&sd)), 0); ok == 0 {
		return "", fmt.Errorf("util: convert the DACL naming %s: %w", sid, callErr)
	}
	defer syscall.LocalFree(syscall.Handle(sd)) //nolint:errcheck // frees the converted descriptor
	rendered, err := formatDACL(sd, sid)
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimSuffix(rendered, ")"), ";")
	return fields[len(fields)-1], nil
}

// ownerOnlyEntries judges a DACL in SDDL form: every access-allowed entry must name one of user
// (the account's SID and the alias the system writes it under), SYSTEM (SY), the Administrators
// group (BA) or the owner rights (OW).
func ownerOnlyEntries(sddl string, user ...string) (bool, string, error) {
	body, ok := strings.CutPrefix(sddl, "D:")
	if !ok {
		return false, "", fmt.Errorf("util: %q is not a DACL", sddl)
	}
	if strings.HasPrefix(body, "NO_ACCESS_CONTROL") {
		return false, "everyone (null DACL)", nil
	}
	allowed := map[string]bool{"SY": true, "BA": true, "OW": true}
	for i := 0; i < len(user) && i <= maxDACLEntries; i++ {
		allowed[user[i]] = true
	}
	entries := strings.Split(body, "(")
	for i := 1; i < len(entries) && i <= maxDACLEntries; i++ {
		fields := strings.Split(strings.TrimSuffix(entries[i], ")"), ";")
		if len(fields) < 6 {
			return false, "", fmt.Errorf("util: malformed DACL entry %q", entries[i])
		}
		if fields[0] == "A" && !allowed[fields[5]] {
			return false, fields[5], nil
		}
	}
	return true, "", nil
}
