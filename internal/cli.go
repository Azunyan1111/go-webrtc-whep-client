package internal

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

var (
	WhepURL    string
	VideoCodec string
	ListCodecs bool
	DebugMode  bool
)

func init() {
	pflag.StringVarP(&WhepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	pflag.StringVarP(&VideoCodec, "codec", "c", "h264", "Video codec to use (h264, vp8, vp9)")
	pflag.BoolVarP(&ListCodecs, "list-codecs", "l", false, "List codecs supported by the WHEP server")
	pflag.BoolVarP(&DebugMode, "debug", "d", false, "Enable debug logging")
}

func SetupUsage() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --list-codecs\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}
}
