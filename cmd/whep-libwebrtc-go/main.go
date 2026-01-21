package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal/libwebrtc"
	"github.com/Azunyan1111/go-webrtc-whep-client/internal/mkvwriter"
	"github.com/spf13/pflag"
)

const (
	connectionTimeout      = 30 * time.Second
	mediaTimeout           = 10 * time.Second
	streamTimeout          = 5 * time.Second
	streamTimeoutCheckFreq = 1 * time.Second
)

var (
	whepURL    string
	stunServer string
	debugMode  bool
)

func init() {
	pflag.StringVarP(&stunServer, "stun", "s", "stun:stun.l.google.com:19302", "STUN server URL")
	pflag.BoolVarP(&debugMode, "debug", "d", false, "Enable debug output")
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] <WHEP_URL>\n\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "A WHEP client using libwebrtc for video/audio decoding")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		pflag.PrintDefaults()
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  whep-libwebrtc-go https://example.com/whep | ffplay -i -")
	}

	pflag.Parse()

	args := pflag.Args()
	if len(args) < 1 {
		pflag.Usage()
		os.Exit(1)
	}
	whepURL = args[0]

	if debugMode {
		mkvwriter.DebugLog = func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, "[DEBUG][MKV] "+format, args...)
		}
		libwebrtc.DebugLog = func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, "[DEBUG][LIBWEBRTC] "+format, args...)
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", whepURL)
	fmt.Fprintln(os.Stderr, "Using libwebrtc for WebRTC (native decoding)")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	// Create WebRTC factory
	factory, err := libwebrtc.NewFactory()
	if err != nil {
		return fmt.Errorf("failed to create WebRTC factory: %w", err)
	}
	defer factory.Close()

	// Create MKV writer (RGBA video + encoded Opus audio)
	mkvWriter := mkvwriter.NewEncodedMKVWriter(os.Stdout)

	// State tracking
	var (
		connected     = make(chan struct{})
		connectedOnce sync.Once
		mediaReceived = make(chan struct{})
		mediaOnce     sync.Once
	)

	// Last frame received time for stream timeout detection
	var lastFrameTime atomic.Value
	lastFrameTime.Store(time.Now())
	var lastAudioTime atomic.Value
	lastAudioTime.Store(time.Time{})
	var audioFrameCount atomic.Int64

	// Create callbacks
	callbacks := &libwebrtc.Callbacks{
		OnICEConnectionState: func(state libwebrtc.ICEConnectionState) {
			fmt.Fprintf(os.Stderr, "ICE connection state: %s\n", state)
			if state == libwebrtc.ICEConnectionConnected || state == libwebrtc.ICEConnectionCompleted {
				connectedOnce.Do(func() {
					close(connected)
				})
			}
		},
		OnICEGatheringState: func(state libwebrtc.ICEGatheringState) {
			if debugMode {
				fmt.Fprintf(os.Stderr, "ICE gathering state: %s\n", state)
			}
		},
		OnVideoFrame: func(frame *libwebrtc.VideoFrame) {
			lastFrameTime.Store(time.Now())
			mediaOnce.Do(func() {
				close(mediaReceived)
			})
			if err := mkvWriter.WriteVideoFrame(frame); err != nil {
				if debugMode {
					fmt.Fprintf(os.Stderr, "Video write error: %v\n", err)
				}
			}
		},
		OnAudioFrame: func(frame *libwebrtc.AudioFrame) {
			lastFrameTime.Store(time.Now())
			lastAudioTime.Store(time.Now())
			count := audioFrameCount.Add(1)
			mediaOnce.Do(func() {
				close(mediaReceived)
			})
			if debugMode && (count <= 3 || count%50 == 0) {
				fmt.Fprintf(os.Stderr,
					"[AUDIO][APP] recv: count=%d rate=%dHz channels=%d frames=%d samples=%d ts_us=%d\n",
					count,
					frame.SampleRate,
					frame.Channels,
					frame.Frames,
					len(frame.PCM),
					frame.TimestampUs)
			}
			if err := mkvWriter.WriteAudioFrame(frame); err != nil {
				if debugMode {
					fmt.Fprintf(os.Stderr, "Audio write error: %v\n", err)
				}
			}
		},
	}

	// Create PeerConnection
	pc, err := libwebrtc.NewPeerConnection(factory, stunServer, callbacks)
	if err != nil {
		return fmt.Errorf("failed to create peer connection: %w", err)
	}
	defer pc.Close()

	// Add video and audio transceivers (recvonly)
	if err := pc.AddVideoTransceiver(); err != nil {
		return fmt.Errorf("failed to add video transceiver: %w", err)
	}
	if err := pc.AddAudioTransceiver(); err != nil {
		return fmt.Errorf("failed to add audio transceiver: %w", err)
	}

	// Create offer
	offer, err := pc.CreateOffer()
	if err != nil {
		return fmt.Errorf("failed to create offer: %w", err)
	}

	// Set local description
	if err := pc.SetLocalDescription(offer, "offer"); err != nil {
		return fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete (simple timeout-based approach)
	time.Sleep(500 * time.Millisecond)

	// Get local description with ICE candidates
	localSDP, err := pc.GetLocalDescription()
	if err != nil {
		return fmt.Errorf("failed to get local description: %w", err)
	}

	if debugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Offer ===\n%s\n=== End Offer ===\n\n", localSDP)
	}

	// Send offer to WHEP server
	fmt.Fprintln(os.Stderr, "Sending offer to WHEP server...")
	answer, err := sendOfferToWHEP(localSDP, whepURL)
	if err != nil {
		return fmt.Errorf("WHEP exchange failed: %w", err)
	}

	if debugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Answer ===\n%s\n=== End Answer ===\n\n", answer)
	}

	// Set remote description
	if err := pc.SetRemoteDescription(answer, "answer"); err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	fmt.Fprintln(os.Stderr, "SDP exchange complete, waiting for connection...")

	// Wait for ICE connection
	select {
	case <-sigChan:
		fmt.Fprintln(os.Stderr, "Interrupted during connection...")
		return nil
	case <-connected:
		fmt.Fprintln(os.Stderr, "ICE connected")
	case <-time.After(connectionTimeout):
		return fmt.Errorf("connection timeout after %v", connectionTimeout)
	}

	// Start MKV writer
	writerErrChan := make(chan error, 1)
	go func() {
		if err := mkvWriter.Run(); err != nil {
			writerErrChan <- err
		}
	}()

	fmt.Fprintln(os.Stderr, "Waiting for media stream...")

	// Wait for media
	select {
	case <-sigChan:
		fmt.Fprintln(os.Stderr, "Interrupted while waiting for media...")
		mkvWriter.Close()
		return nil
	case <-mediaReceived:
		fmt.Fprintln(os.Stderr, "Media received, streaming...")
	case err := <-writerErrChan:
		return fmt.Errorf("writer error during startup: %w", err)
	case <-time.After(mediaTimeout):
		mkvWriter.Close()
		return fmt.Errorf("media timeout after %v", mediaTimeout)
	}

	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")
	fmt.Fprintln(os.Stderr, "Piping Matroska (MKV) stream with decoded rawvideo + encoded Opus audio to stdout")
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Stream timeout ticker
	streamTimeoutTicker := time.NewTicker(streamTimeoutCheckFreq)
	defer streamTimeoutTicker.Stop()
	lastAudioLogTime := time.Time{}

	// Main loop with stream timeout detection
	for {
		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "Closing...")
			mkvWriter.Close()
			return nil
		case err := <-writerErrChan:
			mkvWriter.Close()
			if err != nil {
				return fmt.Errorf("writer error: %w", err)
			}
			return nil
		case <-streamTimeoutTicker.C:
			lastTime := lastFrameTime.Load().(time.Time)
			if time.Since(lastTime) > streamTimeout {
				fmt.Fprintln(os.Stderr, "Stream timeout, no frames received...")
				mkvWriter.Close()
				return fmt.Errorf("stream timeout: no frames received for %v", streamTimeout)
			}
			if debugMode {
				now := time.Now()
				if lastAudioLogTime.IsZero() || now.Sub(lastAudioLogTime) >= 5*time.Second {
					lastAudioLogTime = now
					lastAudio := lastAudioTime.Load().(time.Time)
					if lastAudio.IsZero() {
						fmt.Fprintln(os.Stderr, "[AUDIO][APP] no audio frames received yet")
					} else {
						age := now.Sub(lastAudio)
						fmt.Fprintf(os.Stderr, "[AUDIO][APP] last audio %v ago (total=%d)\n", age, audioFrameCount.Load())
					}
				}
			}
		}
	}
}

func sendOfferToWHEP(offer, url string) (string, error) {
	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(offer)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/sdp")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("WHEP server returned status %d: %s", resp.StatusCode, string(body))
	}

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(answer), nil
}
