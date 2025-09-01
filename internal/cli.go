package internal

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

var (
	WhepURL    string
	VideoPipe  bool
	AudioPipe  bool
	VideoCodec string
	ListCodecs bool
	DebugMode  bool
	WebMOutput bool
	SDPOutput  bool
)

func init() {
	pflag.StringVarP(&WhepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	pflag.BoolVarP(&VideoPipe, "video-pipe", "v", false, "Output raw video stream to stdout (for piping to ffplay)")
	pflag.BoolVarP(&AudioPipe, "audio-pipe", "a", false, "Output raw Opus stream to stdout (for piping to ffplay)")
	pflag.StringVarP(&VideoCodec, "codec", "c", "h264", "Video codec to use (h264, vp8, vp9)")
	pflag.BoolVarP(&ListCodecs, "list-codecs", "l", false, "List codecs supported by the WHEP server")
	pflag.BoolVarP(&DebugMode, "debug", "d", false, "Enable debug logging")
	pflag.BoolVarP(&WebMOutput, "webm", "w", false, "Output WebM/Matroska stream with muxed audio and video to stdout")
	pflag.BoolVarP(&SDPOutput, "sdp", "s", false, "Output SDP stream (for debugging and analysis)")
}

func SetupUsage() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --video-pipe | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --audio-pipe | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --list-codecs\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}
}
