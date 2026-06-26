package main

import (
	"fmt"
	"os"
)

// debugEnabled is set once at startup from EC2TAIL_DEBUG. When on, ec2tail emits a verbose trace of
// discovery, the session lifecycle, and every raw line received (before marker filtering) to stderr,
// and stops discarding the SSM library's own log output.
var debugEnabled bool

// tracef writes a "[ec2tail] ..." diagnostic line to stderr when debugging is enabled.
func tracef(format string, args ...any) {
	if !debugEnabled {
		return
	}
	fmt.Fprintf(os.Stderr, "[ec2tail] "+format+"\n", args...)
}
