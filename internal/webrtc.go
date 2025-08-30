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
	var mpegTSMuxer *MPEGTSMuxer
	var webmMuxer *WebMMuxer

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		DebugLog("Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		if MPEGTSOutput || WebMOutput {
			// Store tracks for muxing
			if track.Kind() == webrtc.RTPCodecTypeVideo {
				videoTrack = track
			} else if track.Kind() == webrtc.RTPCodecTypeAudio {
				audioTrack = track
			}

			// Start muxer based on configuration
			if MPEGTSOutput {
				if MPEGTSVideoOnly {
					// Start muxer with video only
					if videoTrack != nil && mpegTSMuxer == nil {
						mpegTSMuxer = NewMPEGTSMuxer(os.Stdout, videoTrack, nil)
						go mpegTSMuxer.Run()
					}
				} else {
					// Start muxer when both tracks are available
					if videoTrack != nil && audioTrack != nil && mpegTSMuxer == nil {
						mpegTSMuxer = NewMPEGTSMuxer(os.Stdout, videoTrack, audioTrack)
						go mpegTSMuxer.Run()
					}
				}
			} else if WebMOutput {
				// Start WebM muxer when both video and audio tracks are available
				// This ensures audio is properly included
				if videoTrack != nil && audioTrack != nil && webmMuxer == nil {
					webmMuxer = NewWebMMuxer(os.Stdout, videoTrack, audioTrack)
					go func() {
						if err := webmMuxer.Run(); err != nil {
							fmt.Fprintf(os.Stderr, "WebM muxer error: %v\n", err)
							os.Exit(1)
						}
					}()
				}
			}
		} else {
			// Original pipe behavior
			if track.Kind() == webrtc.RTPCodecTypeVideo {
				if VideoPipe {
					// Pipe raw video to stdout
					go PipeRawStream(track, os.Stdout, VideoCodec)
				}
			} else {
				if AudioPipe {
					// Pipe raw Opus to stdout
					go PipeRawStream(track, os.Stdout, "")
				}
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
