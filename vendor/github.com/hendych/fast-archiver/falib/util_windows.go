package falib

import (
	"os"
	"time"
)

func (a *Archiver) getModeOwnership(path string) (uid int, gid int, mode os.FileMode, modTime int64) {
	fi, err := os.Lstat(path)
	if err != nil {
		a.Logger.Warning("file stat error; uid/gid/mode will be incorrect:", err.Error())
	} else {
		mode = fi.Mode()
		modTime = fi.ModTime().Unix()
	}
	return
}
