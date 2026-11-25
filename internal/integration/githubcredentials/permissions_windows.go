//go:build windows

package githubcredentials

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

const credentialAllAccess windows.ACCESS_MASK = 0x1F01FF

func credentialSIDs() (*windows.SID, *windows.SID, *windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, nil, nil, err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, nil, nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, nil, err
	}
	return user.User.Sid, system, admins, nil
}

func protectCredentialPath(path string, directory bool) error {
	user, _, _, err := credentialSIDs()
	if err != nil {
		return err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	sd, err := windows.SecurityDescriptorFromString(
		"D:P(A;" + flags + ";FA;;;" + user.String() + ")(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)",
	)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func validateCredentialPath(path string, _ os.FileInfo, directory bool) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return ErrInsecureStorage
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return ErrInsecureStorage
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return ErrInsecureStorage
	}
	user, system, admins, err := credentialSIDs()
	if err != nil {
		return ErrInsecureStorage
	}
	allowed := []*windows.SID{user, system, admins}
	seen := make([]bool, len(allowed))
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return ErrInsecureStorage
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Header.AceFlags&windows.INHERITED_ACE != 0 ||
			ace.Mask != credentialAllAccess {
			return ErrInsecureStorage
		}
		if !directory && ace.Header.AceFlags != 0 {
			return ErrInsecureStorage
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		matched := -1
		for n, candidate := range allowed {
			if sid.Equals(candidate) {
				matched = n
				break
			}
		}
		if matched < 0 || seen[matched] {
			return ErrInsecureStorage
		}
		seen[matched] = true
	}
	for _, ok := range seen {
		if !ok {
			return ErrInsecureStorage
		}
	}
	return nil
}

func syncDirectory(string) error {
	return nil
}
