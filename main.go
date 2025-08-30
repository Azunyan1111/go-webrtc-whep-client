package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/spf13/pflag"
)

func main() {
	internal.SetupUsage()
	pflag.Parse()

	if internal.ListCodecs {
		if err := internal.ListServerCodecs(); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Validate pipe options
	if internal.VideoPipe && internal.AudioPipe {
		return fmt.Errorf("cannot pipe both video and audio to stdout simultaneously")
	}

	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", internal.WhepURL)
	fmt.Fprintf(os.Stderr, "Using video codec: %s\n", internal.VideoCodec)

	// Create MediaEngine with selected codec
	mediaEngine, err := internal.CreateMediaEngine(internal.VideoCodec)
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

	if internal.VideoPipe {
		fmt.Fprintf(os.Stderr, "Piping raw %s video to stdout\n", strings.ToUpper(internal.VideoCodec))
	}

	if internal.AudioPipe {
		fmt.Fprintln(os.Stderr, "Piping raw Opus audio to stdout")
	}

	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(os.Stderr, "Closing...")
	return nil
}