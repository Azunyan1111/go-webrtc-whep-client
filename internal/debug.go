package internal

import (
	"fmt"
	"os"
)

// debugLog prints debug messages only when debug mode is enabled
func DebugLog(format string, v ...interface{}) {
	if DebugMode {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format, v...)
	}
}
