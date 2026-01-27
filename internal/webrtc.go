package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/videoframe"
	"github.com/pion/webrtc/v4"
)

// ConnectionState は接続状態を表す
type ConnectionState int

const (
	StateConnecting ConnectionState = iota
	StateConnected
	StateDisconnected
	StateFailed
)

// ConnectionEvent は接続イベントを通知する構造体
type ConnectionEvent struct {
	State ConnectionState
	Error error
}

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

func CreatePeerConnection(mediaEngine *webrtc.MediaEngine, eventChan chan<- ConnectionEvent, streamManager *StreamManager) (*webrtc.PeerConnection, error) {
	// Create an InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return nil, err
	}

	// Register videoframe interceptor for VP8 frame assembly
	vfFactory, err := videoframe.NewReceiverInterceptor()
	if err != nil {
		return nil, fmt.Errorf("failed to create videoframe interceptor: %w", err)
	}
	interceptorRegistry.Add(vfFactory)

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

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		DebugLog("Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			codecType := MimeTypeToCodec(codec.MimeType)
			fmt.Fprintf(os.Stderr, "Video track received: %s\n", codec.MimeType)
			streamManager.AddVideoTrack(track, codecType)
		} else if track.Kind() == webrtc.RTPCodecTypeAudio {
			fmt.Fprintf(os.Stderr, "Audio track received: %s\n", codec.MimeType)
			streamManager.AddAudioTrack(track)
		}
	})

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		DebugLog("ICE Connection State has changed: %s\n", connectionState.String())

		switch connectionState {
		case webrtc.ICEConnectionStateConnected:
			select {
			case eventChan <- ConnectionEvent{State: StateConnected}:
			default:
			}
		case webrtc.ICEConnectionStateFailed:
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
			select {
			case eventChan <- ConnectionEvent{State: StateFailed, Error: fmt.Errorf("ICE connection failed")}:
			default:
			}
		case webrtc.ICEConnectionStateDisconnected:
			select {
			case eventChan <- ConnectionEvent{State: StateDisconnected}:
			default:
			}
		}
	})

	return peerConnection, nil
}
