package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/spf13/pflag"
)

func main() {
	internal.SetupUsage()
	pflag.Parse()

	if err := internal.ParseArgs(); err != nil {
		pflag.Usage()
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", internal.WhepURL)
	fmt.Fprintln(os.Stderr, "Supported video codecs: VP8, VP9")

	// Create MediaEngine with VP8/VP9
	mediaEngine, err := internal.CreateVP8VP9MediaEngine()
	if err != nil {
		return err
	}

	// Create PeerConnection
	peerConnection, err := internal.CreatePeerConnection(mediaEngine)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", cErr)
		}
	}()

	// Exchange SDP with WHEP server
	if err := internal.ExchangeSDPWithWHEP(peerConnection, internal.WhepURL); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")
	fmt.Fprintln(os.Stderr, "Piping Matroska (MKV) stream with decoded rawvideo + Opus audio to stdout")
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(os.Stderr, "Closing...")
	return nil
}
