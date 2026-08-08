// Package localfs rejects known network, userspace, and unknown filesystem
// classes before Hecate performs sensitive in-process reads. This bounds
// repository-selected remote/FUSE exposure; it cannot make a failing physical
// local device synchronously cancellable.
package localfs

import "errors"

var ErrUnboundedFilesystem = errors.New("path is not on a bounded local filesystem")
