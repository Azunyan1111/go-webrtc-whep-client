package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

func CreateMediaEngine(codec string) (*webrtc.MediaEngine, error) {
	mediaEngine := &webrtc.MediaEngine{}

	// Register video codec based on user selection
	switch strings.ToLower(codec) {
	case "h264":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			},
			PayloadType: 96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	case "vp8":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
			},
			PayloadType: 97,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	case "vp9":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP9, ClockRate: 90000,
			},
			PayloadType: 98,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported video codec: %s (supported: h264, vp8, vp9)", codec)
	}

	// Register audio codec (Opus)
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	return mediaEngine, nil
}

func CreatePeerConnection(mediaEngine *webrtc.MediaEngine) (*webrtc.PeerConnection, error) {
	// Create an InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return nil, err
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
		return nil, err
	}

	// Create tracks for receiving
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		peerConnection.Close()
		return nil, err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		peerConnection.Close()
		return nil, err
	}

	// Track storage for muxing
	var videoTrack *webrtc.TrackRemote
	var audioTrack *webrtc.TrackRemote
	var streamManager *StreamManager
	var sdpWriter *SDPStreamWriter

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		DebugLog("Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		// Store tracks
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			videoTrack = track
			fmt.Fprintf(os.Stderr, "Video track received: %s\n", codec.MimeType)
		} else if track.Kind() == webrtc.RTPCodecTypeAudio {
			audioTrack = track
			fmt.Fprintf(os.Stderr, "Audio track received: %s\n", codec.MimeType)
		}

		// Initialize stream manager if not already done
		if streamManager == nil {
			var writer StreamWriter
			processor := NewDefaultRTPProcessor()

			// Create appropriate writer based on output configuration
			if WebMOutput {
				// WebM with both audio and video
				if videoTrack != nil && audioTrack != nil {
					writer = NewWebMStreamWriter(os.Stdout, videoTrack, audioTrack)
					streamManager = NewStreamManager(writer, processor)
					streamManager.AddVideoTrack(videoTrack, VideoCodec)
					streamManager.AddAudioTrack(audioTrack)
					go func() {
						if err := streamManager.Run(); err != nil {
							fmt.Fprintf(os.Stderr, "Stream manager error: %v\n", err)
							os.Exit(1)
						}
					}()
				}
			} else if VideoPipe || AudioPipe || SDPOutput {
				// Raw stream output or SDP output
				if SDPOutput {
					// SDP output mode - create SDP writer and handle RTP packets directly
					if sdpWriter == nil {
						sdpWriter = NewSDPStreamWriter(os.Stdout)

						// Start the SDP writer
						fmt.Fprintf(os.Stderr, "Starting SDP writer\n")
						go func() {
							if err := sdpWriter.Run(); err != nil {
								fmt.Fprintf(os.Stderr, "SDP writer error: %v\n", err)
								os.Exit(1)
							}
						}()
					}

					// Process track based on type
					if track.Kind() == webrtc.RTPCodecTypeVideo {
						fmt.Fprintf(os.Stderr, "Processing video track in SDP mode\n")
						go func() {
							for {
								rtpPacket, _, err := track.ReadRTP()
								if err != nil {
									fmt.Fprintf(os.Stderr, "Error reading video RTP: %v\n", err)
									return
								}
								if err := sdpWriter.WriteVideoRTPPacket(rtpPacket); err != nil {
									fmt.Fprintf(os.Stderr, "Error writing video RTP packet: %v\n", err)
									return
								}
							}
						}()
					} else if track.Kind() == webrtc.RTPCodecTypeAudio {
						fmt.Fprintf(os.Stderr, "Processing audio track in SDP mode\n")
						go func() {
							for {
								rtpPacket, _, err := track.ReadRTP()
								if err != nil {
									fmt.Fprintf(os.Stderr, "Error reading audio RTP: %v\n", err)
									return
								}
								if err := sdpWriter.WriteAudioRTPPacket(rtpPacket); err != nil {
									fmt.Fprintf(os.Stderr, "Error writing audio RTP packet: %v\n", err)
									return
								}
							}
						}()
					}
				} else if track.Kind() == webrtc.RTPCodecTypeVideo && VideoPipe {
					// H264の場合は直接書き込み方式を使用（遅延を避けるため）
					if VideoCodec == "h264" {
						h264Processor := NewH264DirectStreamProcessor(videoTrack, os.Stdout)
						go func() {
							if err := h264Processor.Run(); err != nil {
								fmt.Fprintf(os.Stderr, "H264 processor error: %v\n", err)
								os.Exit(1)
							}
						}()
					} else {
						// VP8/VP9は新しい方式を使用
						writer = NewRawStreamWriter(os.Stdout, VideoCodec)
						streamManager = NewStreamManager(writer, processor)
						streamManager.AddVideoTrack(videoTrack, VideoCodec)
						go func() {
							if err := streamManager.Run(); err != nil {
								fmt.Fprintf(os.Stderr, "Stream manager error: %v\n", err)
								os.Exit(1)
							}
						}()
					}
				} else if track.Kind() == webrtc.RTPCodecTypeAudio && AudioPipe {
					writer = NewRawStreamWriter(os.Stdout, "opus")
					streamManager = NewStreamManager(writer, processor)
					streamManager.AddAudioTrack(audioTrack)
					go func() {
						if err := streamManager.Run(); err != nil {
							fmt.Fprintf(os.Stderr, "Stream manager error: %v\n", err)
							os.Exit(1)
						}
					}()
				}
			}
		} else {
			// Update existing stream manager with new track
			if track.Kind() == webrtc.RTPCodecTypeVideo {
				streamManager.AddVideoTrack(videoTrack, VideoCodec)
			} else if track.Kind() == webrtc.RTPCodecTypeAudio {
				streamManager.AddAudioTrack(audioTrack)
			}
		}
	})

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		DebugLog("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
			os.Exit(1)
		}
	})

	return peerConnection, nil
}

func CreateAllCodecsMediaEngine() (*webrtc.MediaEngine, error) {
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
			return nil, err
		}
	}

	// Register audio codec
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	return mediaEngine, nil
}
