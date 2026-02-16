package internal

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
)

var (
	WhepURL           string
	WhipURL           string
	DebugMode         bool
	NoFrameValidation bool
	NoPacing          bool
	DropThreshold     int // 遅延フレーム破棄閾値（ミリ秒）
	VideoBitrateKbps  int // VP8目標ビットレート（kbps）
	CPUProfilePath    string
	MemProfilePath    string
)

func init() {
	pflag.BoolVarP(&DebugMode, "debug", "d", false, "Enable debug logging")
	pflag.BoolVar(&NoFrameValidation, "no-validate", false, "Disable frame validation (show raw packet loss artifacts)")
	pflag.BoolVar(&NoPacing, "no-pacing", false, "Disable PTS-based pacing (send frames as fast as possible)")
	pflag.IntVar(&DropThreshold, "drop-threshold", 200, "Drop frames that are more than this many milliseconds late (0 to disable)")
	pflag.IntVarP(&VideoBitrateKbps, "video-bitrate-kbps", "b", 5000, "VP8 target video bitrate in kbps")
	pflag.StringVar(&CPUProfilePath, "cpu-profile", "", "Write CPU profile to file (whip-go only)")
	pflag.StringVar(&MemProfilePath, "mem-profile", "", "Write heap profile to file at exit (whip-go only)")
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

func SetupWhipUsage() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHIP Native Client - Send WebRTC streams via WHIP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s <WHIP_URL> [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Arguments:\n")
		fmt.Fprintf(os.Stderr, "  WHIP_URL    WHIP server URL (required)\n\n")
		fmt.Fprintf(os.Stderr, "Input:\n")
		fmt.Fprintf(os.Stderr, "  stdin       MKV stream with rawvideo (RGBA) + Opus audio\n\n")
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  cat video.mkv | %s http://example.com/whip\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  whep-go http://in.example.com/whep | %s http://out.example.com/whip\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}
}

func ParseWhipArgs() error {
	args := pflag.Args()
	if len(args) < 1 {
		return fmt.Errorf("WHIP_URL is required")
	}
	WhipURL = args[0]
	return nil
}
