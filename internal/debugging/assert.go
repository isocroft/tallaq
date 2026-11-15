/* @INFO: Package debugging provides tiny runtime-invariant helpers. */
package debugging

import (
	"fmt"
	"log"
)

/*
@HINT:

`Assert(...)` panics with an "assertion error" when condition is false.

Go has no built-in assert; this is a stop-gap for internal invariants
(never use it in production without a guard clause or use it for expected errors).
*/
func Assert(condition bool, message string) {
	if condition {
		return
	}

	log.Printf("assertion failed: %s", message)
	panic(fmt.Sprintf("assertion error: %s", message))
}

/* @HINT: `Assertf(...)` is `Assert(...)` with a printf-style message. */
func Assertf(condition bool, format string, args ...any) {
	if condition {
		return
	}
	Assert(false, fmt.Sprintf(format, args...))
}
