package falib

import (
	"os"
	"syscall"
)

func (a *Archiver) getModeOwnership(path string) (int, int, os.FileMode, int64) {
	var uid int = 0
	var gid int = 0
	var mode os.FileMode = 0
	var modTime int64 = 0

	fi, err := os.Lstat(path)
	if err != nil {
		a.Logger.Warning("file lstat error; uid/gid/mode will be incorrect:", err.Error())
	} else {
		mode = fi.Mode()
		modTime = fi.ModTime().Unix()
		stat_t := fi.Sys().(*syscall.Stat_t)
		if stat_t != nil {
			uid = int(stat_t.Uid)
			gid = int(stat_t.Gid)
		} else {
			a.Logger.Warning("unable to find file uid/gid")
		}
	}

	return uid, gid, mode, modTime
}
