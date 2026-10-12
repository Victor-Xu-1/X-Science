//go:build windows

package datadir

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func namespaceOwned(path string, info os.FileInfo, _ bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("namespace source is a reparse-point alias")
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var actual windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &actual); err != nil {
		return err
	}
	if actual.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.Mode().IsRegular() && actual.NumberOfLinks != 1) {
		return errors.New("namespace source has a reparse-point or hard-link alias")
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return errors.New("namespace source belongs to a different operating-system owner")
	}
	return nil
}
