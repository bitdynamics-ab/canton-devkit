package cliprefix

import "sync/atomic"

const Default = "dpm localnet"

var current atomic.Pointer[string]

func Get() string {
	if p := current.Load(); p != nil && *p != "" {
		return *p
	}
	return Default
}

func Set(prefix string) (restore func()) {
	prev := current.Swap(&prefix)
	return func() { current.Store(prev) }
}

func Cmd(args string) string {
	return Get() + " " + args
}
