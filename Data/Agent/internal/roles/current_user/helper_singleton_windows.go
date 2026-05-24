//go:build windows

package currentuser

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const helperSingletonWaitAbandoned = 0x00000080

func acquireHelperSingleton(sessionID int) (func(), bool, error) {
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Local\BorealisAgentCurrentUserHelper-%d`, sessionID))
	if err != nil {
		return func() {}, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return func() {}, false, err
	}
	waitResult, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return func() {}, false, err
	}
	if waitResult != windows.WAIT_OBJECT_0 && waitResult != helperSingletonWaitAbandoned {
		_ = windows.CloseHandle(handle)
		return func() {}, false, nil
	}
	release := func() {
		_ = windows.ReleaseMutex(handle)
		_ = windows.CloseHandle(handle)
	}
	return release, true, nil
}
