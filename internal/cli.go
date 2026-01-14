package internal

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

var (
	WhepURL   string
	DebugMode bool
)

func init() {
	pflag.BoolVarP(&DebugMode, "debug", "d", false, "Enable debug logging")
}

func SetupUsage() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s <WHEP_URL> [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  WHEP_URL    WHEP server URL (required)\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s http://example.com/whep | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s http://example.com/whep -d | ffplay -i -\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}
}

func ParseArgs() error {
	args := pflag.Args()
	if len(args) < 1 {
		return fmt.Errorf("WHEP_URL is required")
	}
	WhepURL = args[0]
	return nil
}
