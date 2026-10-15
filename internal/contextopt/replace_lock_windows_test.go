// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package contextopt

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// markClaimPendingDelete leaves the directory's claim file pending deletion, as a writer's
// release does on windows-latest while another handle to the file is still open: the file
// takes a legacy delete disposition and stays until the returned close runs. Windows 11 on
// NTFS deletes with POSIX semantics and never shows this state through os.Remove, so the
// disposition is set directly (#827).
func markClaimPendingDelete(t *testing.T, dir string) (closeHandle func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(filepath.Join(dir, directoryClaimName))
	if err != nil {
		t.Fatal(err)
	}
	const deleteAccess = 0x00010000 // DELETE
	handle, err := syscall.CreateFile(name, deleteAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open claim for delete: %v", err)
	}
	const fileDispositionInfo = 4
	disposition := uint32(1) // FILE_DISPOSITION_INFO.DeleteFile
	setInfo := syscall.NewLazyDLL("kernel32.dll").NewProc("SetFileInformationByHandle")
	if ok, _, callErr := setInfo.Call(uintptr(handle), fileDispositionInfo,
		uintptr(unsafe.Pointer(&disposition)), unsafe.Sizeof(disposition)); ok == 0 {
		_ = syscall.CloseHandle(handle)
		t.Fatalf("mark claim pending delete: %v", callErr)
	}
	closed := false
	closeHandle = func() {
		if !closed {
			closed = true
			if err := syscall.CloseHandle(handle); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(closeHandle)
	return closeHandle
}

// Positive: a claim file whose deletion is still pending is a writer leaving, so the next
// writer waits for it and then claims the directory. Before #827 the create's "Access is
// denied" was a hard "claim snapshot directory" error at the first attempt.
func TestLockDirectory_Positive_WaitsForPendingDeleteClaim(t *testing.T) {
	root, dir := pinnedDirectory(t)
	if err := root.WriteFile(directoryClaimName, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	closeHandle := markClaimPendingDelete(t, dir)
	if _, _, err := tryLockSnapshotDirectory(root); err != nil {
		t.Fatalf("pending-delete claim is an error, not a busy directory: %v", err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		closeHandle()
	}()
	release := mustLock(t, budgetContext(t, 5*time.Second), root)
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
