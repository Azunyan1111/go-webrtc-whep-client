package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/pflag"
)

var (
	WhepURL    string
	VideoCodec string
	ListCodecs bool
	DebugMode  bool
	OutputFormat string
)

const (
	OutputFormatMKV      = "mkv"
	OutputFormatRawVideo = "rawvideo"
)

func init() {
	pflag.StringVarP(&WhepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	pflag.StringVarP(&VideoCodec, "codec", "c", "h264", "Video codec to use (h264, vp8, vp9)")
	pflag.StringVarP(&OutputFormat, "format", "f", OutputFormatMKV, "Output format (mkv, rawvideo)")
	pflag.BoolVarP(&ListCodecs, "list-codecs", "l", false, "List codecs supported by the WHEP server")
	pflag.BoolVarP(&DebugMode, "debug", "d", false, "Enable debug logging")
}

func ValidateOutputFormat() error {
	OutputFormat = strings.ToLower(OutputFormat)
	switch OutputFormat {
	case OutputFormatMKV, OutputFormatRawVideo:
		return nil
	default:
		return fmt.Errorf("unsupported output format: %s (supported: mkv, rawvideo)", OutputFormat)
	}
}

func SetupUsage() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --format rawvideo | ffplay -f h264 -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --list-codecs\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}
}
