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
		return nil, fmt.Errorf("unsupported video codec: %s (supported: vp8, vp9)", codec)
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

func CreateVP8VP9MediaEngine() (*webrtc.MediaEngine, error) {
	mediaEngine := &webrtc.MediaEngine{}

	// Register VP8
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
		},
		PayloadType: 97,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}

	// Register VP9
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP9, ClockRate: 90000,
		},
		PayloadType: 98,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
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

func MimeTypeToCodec(mimeType string) string {
	switch mimeType {
	case webrtc.MimeTypeVP8:
		return "vp8"
	case webrtc.MimeTypeVP9:
		return "vp9"
	default:
		return ""
	}
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
	var codecType string

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		DebugLog("Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		// Store tracks
		if track.Kind() == webrtc.RTPCodecTypeVideo {
			videoTrack = track
			codecType = MimeTypeToCodec(codec.MimeType)
			fmt.Fprintf(os.Stderr, "Video track received: %s\n", codec.MimeType)
		} else if track.Kind() == webrtc.RTPCodecTypeAudio {
			audioTrack = track
			fmt.Fprintf(os.Stderr, "Audio track received: %s\n", codec.MimeType)
		}

		// Initialize stream manager when both tracks are available
		if streamManager == nil && videoTrack != nil && audioTrack != nil {
			processor := NewDefaultRTPProcessor()

			// Always use RawVideoMKVWriter
			writer := NewRawVideoMKVWriter(os.Stdout, codecType)
			streamManager = NewStreamManager(writer, processor)
			streamManager.AddVideoTrack(videoTrack, codecType)
			streamManager.AddAudioTrack(audioTrack)

			go func() {
				if err := streamManager.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "Stream manager error: %v\n", err)
					os.Exit(1)
				}
			}()
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
