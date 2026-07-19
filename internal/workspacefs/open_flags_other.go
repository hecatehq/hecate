//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !hurd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package workspacefs

// Windows workspace paths cannot address named pipes through os.Root. Keep the
// shared open path portable while Unix uses O_NONBLOCK to make FIFO races safe.
const nonBlockingReadFlag = 0
