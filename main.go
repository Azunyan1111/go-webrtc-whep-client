package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/spf13/pflag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var (
	whepURL    string
	videoPipe  bool
	audioPipe  bool
	videoCodec string
	listCodecs bool
)

func init() {
	pflag.StringVarP(&whepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	pflag.BoolVarP(&videoPipe, "video-pipe", "v", false, "Output raw video stream to stdout (for piping to ffmpeg)")
	pflag.BoolVarP(&audioPipe, "audio-pipe", "a", false, "Output raw Opus stream to stdout (for piping to ffmpeg)")
	pflag.StringVarP(&videoCodec, "codec", "c", "h264", "Video codec to use (h264, vp8, vp9)")
	pflag.BoolVarP(&listCodecs, "list-codecs", "l", false, "List codecs supported by the WHEP server")
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --video-pipe | ffmpeg -i - -c copy output.mp4\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --audio-pipe | ffmpeg -i - -c copy output.mp3\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --list-codecs\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}

	pflag.Parse()

	if listCodecs {
		if err := listServerCodecs(); err != nil {
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
	if videoPipe && audioPipe {
		return fmt.Errorf("cannot pipe both video and audio to stdout simultaneously")
	}

	if videoPipe && audioPipe {
		return fmt.Errorf("cannot output both video and audio to stdout")
	}

	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", whepURL)
	fmt.Fprintf(os.Stderr, "Using video codec: %s\n", videoCodec)

	// Create a MediaEngine object to configure the supported codec
	mediaEngine := &webrtc.MediaEngine{}

	// Register video codec based on user selection
	switch strings.ToLower(videoCodec) {
	case "h264":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			},
			PayloadType: 96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	case "vp8":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
			},
			PayloadType: 97,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	case "vp9":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP9, ClockRate: 90000,
			},
			PayloadType: 98,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported video codec: %s (supported: h264, vp8, vp9)", videoCodec)
	}

	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create an InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	// Create the API object
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create a new PeerConnection
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
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", cErr)
		}
	}()

	// Create tracks for receiving
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		return err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		return err
	}

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		fmt.Fprintf(os.Stderr, "Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			if videoPipe {
				// Pipe raw video to stdout
				go pipeRawStream(track, os.Stdout, videoCodec)
			}
		} else {
			if audioPipe {
				// Pipe raw Opus to stdout
				go pipeRawStream(track, os.Stdout, "")
			}
		}
	})

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Fprintf(os.Stderr, "ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
			os.Exit(1)
		}
	})

	// Create offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	// Create gathering complete promise
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Set local description
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		return err
	}

	// Wait for ICE gathering to complete
	<-gatherComplete

	// Send offer to WHEP server
	fmt.Fprintln(os.Stderr, "Sending offer to WHEP server...")
	fmt.Fprintf(os.Stderr, "\n=== SDP Offer ===\n%s\n=== End Offer ===\n\n", peerConnection.LocalDescription().SDP)

	// Create HTTP request
	req, err := http.NewRequest("POST", whepURL, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
	if err != nil {
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/sdp")

	// Send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("WHEP server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read answer
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Set remote description
	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\n=== SDP Answer ===\n%s\n=== End Answer ===\n\n", string(answer))
	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")

	if videoPipe {
		fmt.Fprintf(os.Stderr, "Piping raw %s video to stdout\n", strings.ToUpper(videoCodec))
	}

	if audioPipe {
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

func listServerCodecs() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server to retrieve supported codecs: %s\n", whepURL)

	// Create a MediaEngine with all possible codecs
	mediaEngine := &webrtc.MediaEngine{}

	// Register all video codecs
	videoCodecs := []struct {
		mimeType    string
		payloadType uint8
	}{
		{webrtc.MimeTypeH264, 96},
		{webrtc.MimeTypeVP8, 97},
		{webrtc.MimeTypeVP9, 98},
	}

	for _, codec := range videoCodecs {
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: codec.mimeType, ClockRate: 90000,
			},
			PayloadType: webrtc.PayloadType(codec.payloadType),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	// Register audio codec
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create interceptor registry and API
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create peer connection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer peerConnection.Close()

	// Add transceivers
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}

	// Create offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		return err
	}

	<-gatherComplete

	// Send offer to WHEP server
	req, err := http.NewRequest("POST", whepURL, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/sdp")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("WHEP server returned status %d: %s", resp.StatusCode, string(body))
	}

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	}); err != nil {
		return err
	}

	// Get negotiated codecs from transceivers
	fmt.Println("\nSupported codecs by WHEP server:")
	fmt.Println("\nVideo codecs:")

	transceivers := peerConnection.GetTransceivers()
	for _, transceiver := range transceivers {
		if transceiver.Kind() == webrtc.RTPCodecTypeVideo {
			codecs := transceiver.Receiver().GetParameters().Codecs
			for _, codec := range codecs {
				fmt.Printf("  - %s (payload type: %d, clock rate: %d)\n",
					codec.MimeType, codec.PayloadType, codec.ClockRate)
			}
		}
	}

	fmt.Println("\nAudio codecs:")
	for _, transceiver := range transceivers {
		if transceiver.Kind() == webrtc.RTPCodecTypeAudio {
			codecs := transceiver.Receiver().GetParameters().Codecs
			for _, codec := range codecs {
				fmt.Printf("  - %s (payload type: %d, clock rate: %d, channels: %d)\n",
					codec.MimeType, codec.PayloadType, codec.ClockRate, codec.Channels)
			}
		}
	}

	return nil
}

// pipeRawStream pipes raw codec data to a writer (typically stdout for ffmpeg)
func pipeRawStream(track *webrtc.TrackRemote, w io.Writer, codecType string) {
	// Buffer for accumulating NAL units
	var nalBuffer []byte

	// For VP8/VP9 IVF output
	var ivfHeaderWritten bool
	var frameCount uint32
	var firstTimestamp uint32
	var currentFrame []byte
	var seenKeyFrame bool

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading RTP packet: %v\n", err)
			return
		}

		// Extract payload
		payload := rtpPacket.Payload

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			switch strings.ToLower(codecType) {
			case "h264":
				// For H264, we need to handle NAL units properly
				// This is a simplified version - production code should handle:
				// - STAP-A (Single Time Aggregation Packet)
				// - FU-A (Fragmentation Unit)
				// - Parameter sets (SPS/PPS)

				if len(payload) < 1 {
					continue
				}

				nalType := payload[0] & 0x1F

				// Check for start of new NAL unit
				if nalType >= 1 && nalType <= 23 {
					// Single NAL unit packet
					// Write start code
					startCode := []byte{0x00, 0x00, 0x00, 0x01}
					if _, err := w.Write(startCode); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing start code: %v\n", err)
						return
					}

					// Write NAL unit
					if _, err := w.Write(payload); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
						return
					}
				} else if nalType == 28 {
					// FU-A fragmentation
					if len(payload) < 2 {
						continue
					}

					fuHeader := payload[1]
					isStart := (fuHeader & 0x80) != 0
					isEnd := (fuHeader & 0x40) != 0

					if isStart {
						// Start of fragmented NAL
						nalBuffer = nil
						// Reconstruct NAL header
						nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
						nalBuffer = append(nalBuffer, nalHeader)
					}

					// Append fragment data
					if len(payload) > 2 {
						nalBuffer = append(nalBuffer, payload[2:]...)
					}

					if isEnd && len(nalBuffer) > 0 {
						// End of fragmented NAL - write it out
						startCode := []byte{0x00, 0x00, 0x00, 0x01}
						if _, err := w.Write(startCode); err != nil {
							fmt.Fprintf(os.Stderr, "Error writing start code: %v\n", err)
							return
						}

						if _, err := w.Write(nalBuffer); err != nil {
							fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
							return
						}
						nalBuffer = nil
					}
				}
			case "vp8":
				// Write IVF header on first packet
				if !ivfHeaderWritten {
					header := make([]byte, 32)
					copy(header[0:], "DKIF")                        // Signature
					binary.LittleEndian.PutUint16(header[4:], 0)    // Version
					binary.LittleEndian.PutUint16(header[6:], 32)   // Header size
					copy(header[8:], "VP80")                        // FourCC
					binary.LittleEndian.PutUint16(header[12:], 640) // Width
					binary.LittleEndian.PutUint16(header[14:], 480) // Height
					binary.LittleEndian.PutUint32(header[16:], 30)  // Framerate denominator
					binary.LittleEndian.PutUint32(header[20:], 1)   // Framerate numerator
					binary.LittleEndian.PutUint32(header[24:], 0)   // Frame count (placeholder)
					binary.LittleEndian.PutUint32(header[28:], 0)   // Unused

					if _, err := w.Write(header); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF header: %v\n", err)
						return
					}
					ivfHeaderWritten = true
					firstTimestamp = rtpPacket.Timestamp
				}

				// Parse VP8 payload
				if len(payload) < 1 {
					continue
				}

				// VP8 payload descriptor
				var vp8Header int
				headerSize := 1

				// X bit check
				if payload[0]&0x80 != 0 {
					headerSize++
					if len(payload) < headerSize {
						continue
					}
					vp8Header = 1
				}

				// Check S bit for start of partition
				isStart := (payload[0] & 0x10) != 0

				// Skip VP8 payload descriptor
				if len(payload) <= vp8Header {
					continue
				}

				payloadData := payload[headerSize:]

				// For VP8, check if this is a keyframe
				if isStart && len(payloadData) >= 3 {
					// VP8 bitstream - check P bit in first byte
					isKeyFrame := (payloadData[0] & 0x01) == 0
					if !seenKeyFrame && !isKeyFrame {
						continue
					}
					seenKeyFrame = true
				}

				// Accumulate frame data
				if isStart {
					currentFrame = nil
				}
				currentFrame = append(currentFrame, payloadData...)

				// Write frame when marker bit is set
				if rtpPacket.Marker && len(currentFrame) > 0 {
					// Write IVF frame header
					frameHeader := make([]byte, 12)
					binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(currentFrame)))
					// Calculate timestamp
					timestamp := (rtpPacket.Timestamp - firstTimestamp) / 90 // Convert from 90kHz to milliseconds
					binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

					if _, err := w.Write(frameHeader); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
						return
					}

					if _, err := w.Write(currentFrame); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing VP8 frame: %v\n", err)
						return
					}

					frameCount++
					currentFrame = nil
				}
			case "vp9":
				// Write IVF header on first packet
				if !ivfHeaderWritten {
					header := make([]byte, 32)
					copy(header[0:], "DKIF")                        // Signature
					binary.LittleEndian.PutUint16(header[4:], 0)    // Version
					binary.LittleEndian.PutUint16(header[6:], 32)   // Header size
					copy(header[8:], "VP90")                        // FourCC
					binary.LittleEndian.PutUint16(header[12:], 640) // Width
					binary.LittleEndian.PutUint16(header[14:], 480) // Height
					binary.LittleEndian.PutUint32(header[16:], 30)  // Framerate denominator
					binary.LittleEndian.PutUint32(header[20:], 1)   // Framerate numerator
					binary.LittleEndian.PutUint32(header[24:], 0)   // Frame count (placeholder)
					binary.LittleEndian.PutUint32(header[28:], 0)   // Unused

					if _, err := w.Write(header); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF header: %v\n", err)
						return
					}
					ivfHeaderWritten = true
					firstTimestamp = rtpPacket.Timestamp
				}

				// Parse VP9 payload
				if len(payload) < 1 {
					continue
				}

				// VP9 payload descriptor
				headerSize := 1

				// I bit check (extended picture ID)
				if payload[0]&0x80 != 0 {
					headerSize++
					if len(payload) < headerSize {
						continue
					}
					// M bit check (extended picture ID is 2 bytes)
					if payload[1]&0x80 != 0 {
						headerSize++
					}
				}

				// L bit check (TID/SLID present)
				if payload[0]&0x40 != 0 {
					headerSize++
				}

				// Check if we have flexible mode
				if payload[0]&0x10 != 0 {
					// F=1, flexible mode
					headerSize++ // For TID/U/SID/D
				}

				if len(payload) < headerSize {
					continue
				}

				// B bit - beginning of frame
				isStart := (payload[0] & 0x08) != 0
				// E bit - end of frame
				isEnd := (payload[0] & 0x04) != 0
				// P bit - inter-picture predicted frame
				isInterFrame := (payload[0] & 0x01) != 0

				// Skip VP9 payload descriptor
				payloadData := payload[headerSize:]

				// Check if keyframe
				if isStart && !isInterFrame {
					seenKeyFrame = true
				} else if !seenKeyFrame {
					continue
				}

				// Accumulate frame data
				if isStart {
					currentFrame = nil
				}
				currentFrame = append(currentFrame, payloadData...)

				// Write frame when we have end bit or marker
				if (isEnd || rtpPacket.Marker) && len(currentFrame) > 0 {
					// Write IVF frame header
					frameHeader := make([]byte, 12)
					binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(currentFrame)))
					// Calculate timestamp
					timestamp := (rtpPacket.Timestamp - firstTimestamp) / 90 // Convert from 90kHz to milliseconds
					binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

					if _, err := w.Write(frameHeader); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
						return
					}

					if _, err := w.Write(currentFrame); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing VP9 frame: %v\n", err)
						return
					}

					frameCount++
					currentFrame = nil
				}
			default:
				// For other codecs, write raw payload
				if _, err := w.Write(payload); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing video payload: %v\n", err)
					return
				}
			}
		} else {
			// For audio (Opus), write raw payload
			if _, err := w.Write(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing audio payload: %v\n", err)
				return
			}
		}
	}
}
