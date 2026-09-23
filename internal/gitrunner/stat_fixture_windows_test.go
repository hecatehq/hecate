//go:build windows

package gitrunner

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func ensureStatCheckFixtureCTimeChanged(t *testing.T, path string) {
	t.Helper()
	// Git for Windows uses creation time for stat's ctime. Rewriting bytes does
	// not change it, so give this config-override fixture a detectable ctime.
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	handle := windows.Handle(file.Fd())
	var before windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &before); err != nil {
		t.Fatal(err)
	}
	creation := windows.NsecToFiletime(before.CreationTime.Nanoseconds() + int64(2*time.Second))
	if err := windows.SetFileTime(handle, &creation, nil, nil); err != nil {
		t.Fatal(err)
	}
	var after windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &after); err != nil {
		t.Fatal(err)
	}
	if after.CreationTime.Nanoseconds()/int64(time.Second) == before.CreationTime.Nanoseconds()/int64(time.Second) {
		t.Fatal("stat-check fixture must change creation time across a whole second")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
