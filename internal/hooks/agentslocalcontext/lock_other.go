//go:build !unix

package agentslocalcontext

import (
	"errors"
	"time"
)

// lockFile は flock の無い環境では使えない。状態を使わずに注入し、重複の抑止を無効にしたことを警告する。
func lockFile(string, time.Duration) (func(), error) {
	return nil, errors.New("file locking is not supported on this platform")
}
