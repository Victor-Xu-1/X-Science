//go:build windows

package productstate

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"time"
	"unsafe"
)

func checkOwner(path string, info os.FileInfo) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("private state reparse point is refused")
	}
	security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := security.Owner()
	user, userErr := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || userErr != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return errors.New("private state has foreign ownership")
	}
	return nil
}
func ownershipFacts(path string, _ os.FileInfo) (string, error) {
	security, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	owner, _, err := security.Owner()
	if err != nil || owner == nil {
		return "", errors.New("private state ownership is unavailable")
	}
	return owner.String(), nil
}
func openWindowsFile(path string, access, creation uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, creation, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	info, err := file.Stat()
	if err == nil {
		err = checkOwner(path, info)
	}
	var identity windows.ByHandleFileInformation
	if err == nil {
		err = windows.GetFileInformationByHandle(handle, &identity)
	}
	if err != nil || !info.Mode().IsRegular() || identity.NumberOfLinks != 1 {
		file.Close()
		return nil, errors.New("private state file is unconfirmed")
	}
	return file, nil
}
func openOwnedFile(path string) (*os.File, error) {
	return openWindowsFile(path, windows.GENERIC_READ, windows.OPEN_EXISTING)
}
func lockNamespace(ctx context.Context, path string) (*os.File, error) {
	file, err := openWindowsFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS)
	if err != nil {
		return nil, err
	}
	for {
		var overlap windows.Overlapped
		err = windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlap)
		if err != windows.ERROR_LOCK_VIOLATION {
			break
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(20 * time.Millisecond):
			continue
		}
		break
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func unlockNamespace(file *os.File) {
	var overlap windows.Overlapped
	windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlap)
	file.Close()
}
func syncDirectory(string) error { return nil } // MoveFileEx publishes with WRITE_THROUGH.
func createOwnedFile(path string, mode os.FileMode) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Supply the protected ACL at creation, before any plaintext key bytes exist.
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
func publishMissing(source, target string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
