//go:build !unix

package update

import "syscall"

// installerProcessAttributes はセッションを分ける手段が無い環境では何も指定しない。
func installerProcessAttributes() *syscall.SysProcAttr { return nil }
