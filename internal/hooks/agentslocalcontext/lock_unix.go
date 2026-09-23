//go:build unix

package agentslocalcontext

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// lockFile は path を 0600 で開いて排他の flock を取る。timeout までに取れなければエラーを返す。
// 返した関数でロックを外してファイルを閉じる。プロセスが落ちてもロックはカーネルが外す。
func lockFile(path string, timeout time.Duration) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			_ = file.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, errors.New("timed out waiting for the state lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
