package debugging

import (
	"fmt"
	"log"
	"strconv"
)

/* @HINT: `enabledFlag` can be overridden at build time using -ldflags */
var enabledFlag = "false"

/* @HINT: Enabled controls whether notice logs are printed. */
var Enabled bool

func init() {
	Enabled, _ = strconv.ParseBool(enabledFlag)
}

/* @HINT: `Notice(...)` formats and logs a debug notice message if debugging is enabled. */
func Notice(format string, args ...any) {
	if !Enabled {
		return
	}

	message := fmt.Sprintf(format, args...)
	log.Printf("[NOTICE] %s", message)
}
