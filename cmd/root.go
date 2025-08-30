package cmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"github.com/spf13/cobra"
)

var (
	whepURL     string
	videoOutput string
	audioOutput string
	bearerToken string
	videoPipe   bool
	audioPipe   bool
	rtpDump     bool
)

var rootCmd = &cobra.Command{
	Use:   "whep-client",
	Short: "WHEP Native Client - Receive WebRTC streams via WHEP protocol",
	Long: `WHEP Native Client connects to a WHEP server and saves the received video/audio to disk.

Examples:
  whep-client -u http://example.com/whep -v stream.ivf -a stream.ogg
  whep-client -u http://example.com/whep --video-pipe | ffmpeg -i - -c copy output.mp4
  whep-client -u http://example.com/whep -v - | ffplay -i -`,
	RunE: run,
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.Flags().StringVarP(&whepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	rootCmd.Flags().StringVarP(&videoOutput, "video", "v", "output.ivf", "Output video file path (use '-' for stdout)")
	rootCmd.Flags().StringVarP(&audioOutput, "audio", "a", "output.ogg", "Output audio file path (use '-' for stdout)")
	rootCmd.Flags().StringVarP(&bearerToken, "token", "t", "", "Bearer token for authentication (optional)")
	rootCmd.Flags().BoolVar(&videoPipe, "video-pipe", false, "Output raw H264 stream to stdout (for piping to ffmpeg)")
	rootCmd.Flags().BoolVar(&audioPipe, "audio-pipe", false, "Output raw Opus stream to stdout (for piping to ffmpeg)")
	rootCmd.Flags().BoolVar(&rtpDump, "rtp-dump", true, "Save raw RTP packets to files")
}

func run(cmd *cobra.Command, args []string) error {
	// Validate pipe options
	if videoPipe && audioPipe {
		return fmt.Errorf("cannot pipe both video and audio to stdout simultaneously")
	}
	
	if (videoPipe || videoOutput == "-") && (audioPipe || audioOutput == "-") {
		return fmt.Errorf("cannot output both video and audio to stdout")
	}
	
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", whepURL)
	if bearerToken != "" {
		fmt.Fprintln(os.Stderr, "Using bearer token authentication")
	}
	
	// Create a MediaEngine object to configure the supported codec
	mediaEngine := &webrtc.MediaEngine{}

	// Register codecs - must match what the server supports
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
		},
		PayloadType: 96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return err
	}

	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 97,
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

	// Create writers for saving
	var videoWriter media.Writer
	var audioWriter media.Writer
	
	// Set up video output
	if !videoPipe && videoOutput != "-" {
		videoFile, err := ivfwriter.New(videoOutput)
		if err != nil {
			return err
		}
		videoWriter = videoFile
	}
	
	// Set up audio output
	if !audioPipe && audioOutput != "-" {
		audioFile, err := oggwriter.New(audioOutput, 48000, 2)
		if err != nil {
			return err
		}
		audioWriter = audioFile
	}

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		fmt.Fprintf(os.Stderr, "Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			if videoPipe || videoOutput == "-" {
				// Pipe raw H264 to stdout
				go pipeRawStream(track, os.Stdout)
			} else if videoWriter != nil {
				go saveTrack(videoWriter, track)
			}
			
			// For raw RTP dump
			if rtpDump && !videoPipe && videoOutput != "-" {
				go extractRTP(track, "video", videoOutput)
			}
		} else {
			if audioPipe || audioOutput == "-" {
				// Pipe raw Opus to stdout
				go pipeRawStream(track, os.Stdout)
			} else if audioWriter != nil {
				go saveTrack(audioWriter, track)
			}
			
			// For raw RTP dump
			if rtpDump && !audioPipe && audioOutput != "-" {
				go extractRTP(track, "audio", audioOutput)
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
	
	// Create HTTP request
	req, err := http.NewRequest("POST", whepURL, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
	if err != nil {
		return err
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/sdp")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer " + bearerToken)
	}
	
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

	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")
	
	if videoPipe {
		fmt.Fprintln(os.Stderr, "Piping raw H264 video to stdout")
	} else if videoOutput == "-" {
		fmt.Fprintln(os.Stderr, "Writing IVF video to stdout")
	} else {
		fmt.Fprintf(os.Stderr, "Video output: %s\n", videoOutput)
	}
	
	if audioPipe {
		fmt.Fprintln(os.Stderr, "Piping raw Opus audio to stdout")
	} else if audioOutput == "-" {
		fmt.Fprintln(os.Stderr, "Writing OGG audio to stdout")
	} else {
		fmt.Fprintf(os.Stderr, "Audio output: %s\n", audioOutput)
	}
	
	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(os.Stderr, "Closing...")
	return nil
}

// saveTrack saves the track to a media file
func saveTrack(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() {
		if err := writer.Close(); err != nil {
			fmt.Printf("Error closing writer: %v\n", err)
		}
	}()

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Printf("Error reading RTP: %v\n", err)
			return
		}

		if err := writer.WriteRTP(rtpPacket); err != nil {
			fmt.Printf("Error writing RTP: %v\n", err)
			return
		}
	}
}

// extractRTP demonstrates how to get raw RTP packets for custom processing
func extractRTP(track *webrtc.TrackRemote, trackType string, baseFilename string) {
	// Open file for raw RTP dump (optional)
	// Remove extension from base filename
	base := baseFilename
	if idx := strings.LastIndex(base, "."); idx != -1 {
		base = base[:idx]
	}
	
	rtpFilename := fmt.Sprintf("%s_%s.rtp", base, trackType)
	rtpFile, err := os.Create(rtpFilename)
	if err != nil {
		fmt.Printf("Error creating RTP file: %v\n", err)
		return
	}
	defer rtpFile.Close()
	
	fmt.Printf("Saving raw RTP packets to: %s\n", rtpFilename)

	packetCount := 0
	startTime := time.Now()

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			if err == io.EOF {
				fmt.Printf("%s track ended. Total packets: %d, Duration: %v\n", 
					trackType, packetCount, time.Since(startTime))
				return
			}
			fmt.Printf("Error reading RTP packet: %v\n", err)
			return
		}

		packetCount++

		// Here you can process the raw RTP packet
		// For example, you could:
		// 1. Forward to another system
		// 2. Transcode
		// 3. Analyze
		// 4. Save to custom format

		// Example: Print packet info every 100 packets
		if packetCount%100 == 0 {
			fmt.Printf("%s: Received %d RTP packets, latest timestamp: %d, SSRC: %d\n",
				trackType, packetCount, rtpPacket.Timestamp, rtpPacket.SSRC)
		}

		// Optionally write raw RTP to file
		data, err := rtpPacket.Marshal()
		if err != nil {
			fmt.Printf("Error marshaling RTP packet: %v\n", err)
			continue
		}
		
		// Write packet length (4 bytes) followed by packet data
		lengthBuf := make([]byte, 4)
		lengthBuf[0] = byte(len(data) >> 24)
		lengthBuf[1] = byte(len(data) >> 16)
		lengthBuf[2] = byte(len(data) >> 8)
		lengthBuf[3] = byte(len(data))
		
		rtpFile.Write(lengthBuf)
		rtpFile.Write(data)
	}
}

// pipeRawStream pipes raw codec data to a writer (typically stdout for ffmpeg)
func pipeRawStream(track *webrtc.TrackRemote, w io.Writer) {
	// Buffer for accumulating NAL units
	var nalBuffer []byte
	
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
		} else {
			// For audio (Opus), write raw payload
			if _, err := w.Write(payload); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing audio payload: %v\n", err)
				return
			}
		}
	}
}