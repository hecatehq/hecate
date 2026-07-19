//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package workspacefs

import "golang.org/x/sys/unix"

const nonBlockingReadFlag = unix.O_NONBLOCK
