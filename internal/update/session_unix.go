//go:build unix

package update

import "syscall"

// installerProcessAttributes は新しいセッションでインストーラーを起動させ、制御端末を継承させない。
func installerProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
