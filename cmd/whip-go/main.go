package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Azunyan1111/go-webrtc-whep-client/internal"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/spf13/pflag"
)

func main() {
	internal.SetupWhipUsage()
	pflag.Parse()

	if err := internal.ParseWhipArgs(); err != nil {
		pflag.Usage()
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHIP server: %s\n", internal.WhipURL)
	fmt.Fprintln(os.Stderr, "Reading MKV from stdin (rawvideo + Opus)")

	// Create MKV reader
	mkvReader := internal.NewMKVReader(os.Stdin)

	// Wait for track info
	fmt.Fprintln(os.Stderr, "Waiting for first video frame to determine resolution...")

	// Read first video frame to get dimensions
	var firstFrame *internal.Frame
	for {
		frame, err := mkvReader.ReadFrame()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("no video frames found in input")
			}
			return fmt.Errorf("failed to read frame: %v", err)
		}
		if frame.Type == internal.FrameTypeVideo {
			firstFrame = frame
			break
		}
	}

	width := mkvReader.VideoWidth()
	height := mkvReader.VideoHeight()
	if width == 0 || height == 0 {
		return fmt.Errorf("could not determine video dimensions")
	}
	pixelFormat := mkvReader.PixelFormat()
	fmt.Fprintf(os.Stderr, "Video resolution: %dx%d, pixel format: %s\n", width, height, pixelFormat)

	// Check audio codec
	audioCodec := mkvReader.AudioCodec()
	needsOpusEncode := (audioCodec == "A_PCM/INT/LIT")
	if audioCodec != "" {
		fmt.Fprintf(os.Stderr, "Audio codec: %s\n", audioCodec)
		if needsOpusEncode {
			fmt.Fprintf(os.Stderr, "PCM audio detected, will encode to Opus\n")
		}
	}

	// Create Opus encoder if needed
	var opusEncoder *internal.OpusEncoder
	if needsOpusEncode {
		sampleRate := mkvReader.AudioSampleRate()
		channels := mkvReader.AudioChannels()
		if sampleRate == 0 {
			sampleRate = 48000
		}
		if channels == 0 {
			channels = 2
		}
		fmt.Fprintf(os.Stderr, "Audio: %dHz, %d channels\n", sampleRate, channels)
		var opusErr error
		opusEncoder, opusErr = internal.NewOpusEncoder(sampleRate, channels)
		if opusErr != nil {
			return fmt.Errorf("failed to create Opus encoder: %v", opusErr)
		}
		defer opusEncoder.Close()
	}

	// Create VP8 encoder
	encoder, err := internal.NewVP8Encoder(width, height, pixelFormat)
	if err != nil {
		return fmt.Errorf("failed to create VP8 encoder: %v", err)
	}
	defer encoder.Close()

	// Create MediaEngine
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		},
		PayloadType: 97,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return err
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	// Create API
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create PeerConnection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer peerConnection.Close()

	// Create video track
	videoTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video", "whip-go",
	)
	if err != nil {
		return err
	}
	if _, err = peerConnection.AddTrack(videoTrack); err != nil {
		return err
	}

	// Create audio track
	audioTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "whip-go",
	)
	if err != nil {
		return err
	}
	if _, err = peerConnection.AddTrack(audioTrack); err != nil {
		return err
	}

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		internal.DebugLog("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
		}
	})

	// Exchange SDP with WHIP server
	if err := internal.ExchangeSDPWithWHIP(peerConnection, internal.WhipURL); err != nil {
		return fmt.Errorf("failed to exchange SDP: %v", err)
	}

	fmt.Fprintln(os.Stderr, "Connected to WHIP server, sending media...")
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Create packetizers
	rand.Seed(time.Now().UnixNano())
	videoPacketizer := internal.NewVP8Packetizer(rand.Uint32())
	audioPacketizer := internal.NewOpusPacketizer(rand.Uint32())

	// Handle interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	stopChan := make(chan struct{})
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "Stopping...")
		close(stopChan)
	}()

	// Process first frame
	if firstFrame.Type == internal.FrameTypeVideo {
		if err := processVideoFrame(firstFrame, encoder, videoPacketizer, videoTrack); err != nil {
			internal.DebugLog("Error processing video frame: %v\n", err)
		}
	}

	// Main loop
	videoCount := 1
	audioCount := 0
	for {
		select {
		case <-stopChan:
			fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
			return nil
		default:
		}

		frame, err := mkvReader.ReadFrame()
		if err != nil {
			if err == io.EOF {
				fmt.Fprintf(os.Stderr, "End of input stream\n")
				fmt.Fprintf(os.Stderr, "Sent %d video frames, %d audio frames\n", videoCount, audioCount)
				return nil
			}
			return fmt.Errorf("failed to read frame: %v", err)
		}

		switch frame.Type {
		case internal.FrameTypeVideo:
			if err := processVideoFrame(frame, encoder, videoPacketizer, videoTrack); err != nil {
				internal.DebugLog("Error processing video frame: %v\n", err)
				continue
			}
			videoCount++
			if videoCount%100 == 0 {
				internal.DebugLog("Sent %d video frames\n", videoCount)
			}

		case internal.FrameTypeAudio:
			if needsOpusEncode && opusEncoder != nil {
				// PCM -> Opus encoding
				encodedFrames, err := opusEncoder.Encode(frame.Data, frame.TimestampMs)
				if err != nil {
					internal.DebugLog("Error encoding audio: %v\n", err)
					continue
				}
				for _, encoded := range encodedFrames {
					// Use the timestamp from each encoded frame (increments by 10ms per frame)
					packet := audioPacketizer.Packetize(encoded.Data, encoded.TimestampMs)
					if packet != nil {
						if err := audioTrack.WriteRTP(packet); err != nil {
							internal.DebugLog("Error writing audio RTP: %v\n", err)
						}
					}
				}
			} else {
				// Already Opus, pass through
				packet := audioPacketizer.Packetize(frame.Data, frame.TimestampMs)
				if packet != nil {
					if err := audioTrack.WriteRTP(packet); err != nil {
						internal.DebugLog("Error writing audio RTP: %v\n", err)
					}
				}
			}
			audioCount++
		}
	}
}

func processVideoFrame(frame *internal.Frame, encoder *internal.VP8Encoder, packetizer *internal.VP8Packetizer, track *webrtc.TrackLocalStaticRTP) error {
	// Encode RGBA to VP8
	encoded, isKeyframe, err := encoder.Encode(frame.Data)
	if err != nil {
		return fmt.Errorf("encode error: %v", err)
	}
	if encoded == nil {
		return nil
	}

	// Packetize and send
	packets := packetizer.Packetize(encoded, frame.TimestampMs, isKeyframe)
	for _, packet := range packets {
		if err := track.WriteRTP(packet); err != nil {
			return fmt.Errorf("write RTP error: %v", err)
		}
	}

	return nil
}
