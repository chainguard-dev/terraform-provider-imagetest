package emitter

import "unsafe"

func stob(s string) (b []byte) {
	if len(s) == 0 {
		return nil
	}
	sd := unsafe.StringData(s)
	return unsafe.Slice(sd, len(s))
}
