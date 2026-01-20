// Package libwebrtc provides Go bindings for libwebrtc via CGO
package libwebrtc

/*
#include "webrtc_objc_wrapper.h"
#include <stdlib.h>

// Forward declarations for Go callbacks
void goOnICEState(uintptr_t userData, int state);
void goOnICEGathering(uintptr_t userData, int state);
void goOnVideoFrame(uintptr_t userData,
    const uint8_t* dataY, int strideY,
    const uint8_t* dataU, int strideU,
    const uint8_t* dataV, int strideV,
    int width, int height, int64_t timestamp_us);
void goOnAudioData(uintptr_t userData,
    const int16_t* data, int sampleRate,
    int channels, int frames, int64_t timestamp_us);
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// ICEConnectionState represents ICE connection state
type ICEConnectionState int

const (
	ICEConnectionNew          ICEConnectionState = 0
	ICEConnectionChecking     ICEConnectionState = 1
	ICEConnectionConnected    ICEConnectionState = 2
	ICEConnectionCompleted    ICEConnectionState = 3
	ICEConnectionFailed       ICEConnectionState = 4
	ICEConnectionDisconnected ICEConnectionState = 5
	ICEConnectionClosed       ICEConnectionState = 6
)

func (s ICEConnectionState) String() string {
	switch s {
	case ICEConnectionNew:
		return "new"
	case ICEConnectionChecking:
		return "checking"
	case ICEConnectionConnected:
		return "connected"
	case ICEConnectionCompleted:
		return "completed"
	case ICEConnectionFailed:
		return "failed"
	case ICEConnectionDisconnected:
		return "disconnected"
	case ICEConnectionClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// ICEGatheringState represents ICE gathering state
type ICEGatheringState int

const (
	ICEGatheringNew       ICEGatheringState = 0
	ICEGatheringGathering ICEGatheringState = 1
	ICEGatheringComplete  ICEGatheringState = 2
)

func (s ICEGatheringState) String() string {
	switch s {
	case ICEGatheringNew:
		return "new"
	case ICEGatheringGathering:
		return "gathering"
	case ICEGatheringComplete:
		return "complete"
	default:
		return "unknown"
	}
}

// VideoFrame represents a decoded video frame in I420 format
type VideoFrame struct {
	DataY       []byte
	DataU       []byte
	DataV       []byte
	StrideY     int
	StrideU     int
	StrideV     int
	Width       int
	Height      int
	TimestampUs int64 // Timestamp in microseconds
}

// AudioFrame represents decoded audio data in PCM format (for compatibility)
type AudioFrame struct {
	PCM         []int16
	SampleRate  int
	Channels    int
	Frames      int
	TimestampUs int64 // Timestamp in microseconds
}

// Callbacks holds callback functions for a PeerConnection
type Callbacks struct {
	OnICEConnectionState func(ICEConnectionState)
	OnICEGatheringState  func(ICEGatheringState)
	OnVideoFrame         func(*VideoFrame)
	OnAudioFrame         func(*AudioFrame)
}

// Global callback registry
var (
	callbacksMu    sync.RWMutex
	callbacksMap           = make(map[uintptr]*Callbacks)
	nextCallbackID uintptr = 1
)

func registerCallbacks(cb *Callbacks) uintptr {
	callbacksMu.Lock()
	defer callbacksMu.Unlock()
	id := nextCallbackID
	nextCallbackID++
	callbacksMap[id] = cb
	return id
}

func unregisterCallbacks(id uintptr) {
	callbacksMu.Lock()
	defer callbacksMu.Unlock()
	delete(callbacksMap, id)
}

func getCallbacks(id uintptr) *Callbacks {
	callbacksMu.RLock()
	defer callbacksMu.RUnlock()
	return callbacksMap[id]
}

// Factory represents a WebRTC PeerConnection factory
type Factory struct {
	handle C.WebRTCFactoryHandle
}

// NewFactory creates a new WebRTC factory
func NewFactory() (*Factory, error) {
	handle := C.webrtc_objc_factory_create()
	if handle == nil {
		return nil, errors.New("failed to create WebRTC factory")
	}
	return &Factory{handle: handle}, nil
}

// Close destroys the factory
func (f *Factory) Close() {
	if f.handle != nil {
		C.webrtc_objc_factory_destroy(f.handle)
		f.handle = nil
	}
}

// PeerConnection represents a WebRTC peer connection
type PeerConnection struct {
	handle     C.PeerConnectionHandle
	callbackID uintptr
	callbacks  *Callbacks
}

// NewPeerConnection creates a new peer connection
func NewPeerConnection(factory *Factory, stunServer string, callbacks *Callbacks) (*PeerConnection, error) {
	if factory == nil || factory.handle == nil {
		return nil, errors.New("invalid factory")
	}

	callbackID := registerCallbacks(callbacks)

	var stunStr *C.char
	if stunServer != "" {
		stunStr = C.CString(stunServer)
		defer C.free(unsafe.Pointer(stunStr))
	}

	handle := C.webrtc_objc_pc_create(
		factory.handle,
		stunStr,
		C.uintptr_t(callbackID),
		C.OnICEStateCallback(C.goOnICEState),
		C.OnICEGatheringStateCallback(C.goOnICEGathering),
		C.OnVideoFrameCallback(C.goOnVideoFrame),
		C.OnAudioDataCallback(C.goOnAudioData),
	)

	if handle == nil {
		unregisterCallbacks(callbackID)
		return nil, errors.New("failed to create peer connection")
	}

	return &PeerConnection{
		handle:     handle,
		callbackID: callbackID,
		callbacks:  callbacks,
	}, nil
}

// AddVideoTransceiver adds a video transceiver in recvonly mode
func (pc *PeerConnection) AddVideoTransceiver() error {
	if pc.handle == nil {
		return errors.New("peer connection closed")
	}
	if C.webrtc_objc_pc_add_video_transceiver(pc.handle) != 0 {
		return errors.New("failed to add video transceiver")
	}
	return nil
}

// AddAudioTransceiver adds an audio transceiver in recvonly mode
func (pc *PeerConnection) AddAudioTransceiver() error {
	if pc.handle == nil {
		return errors.New("peer connection closed")
	}
	if C.webrtc_objc_pc_add_audio_transceiver(pc.handle) != 0 {
		return errors.New("failed to add audio transceiver")
	}
	return nil
}

// CreateOffer creates an SDP offer
func (pc *PeerConnection) CreateOffer() (string, error) {
	if pc.handle == nil {
		return "", errors.New("peer connection closed")
	}

	sdpC := C.webrtc_objc_pc_create_offer(pc.handle)
	if sdpC == nil {
		return "", errors.New("failed to create offer")
	}
	defer C.webrtc_objc_free_string(sdpC)

	return C.GoString(sdpC), nil
}

// SetLocalDescription sets the local description
func (pc *PeerConnection) SetLocalDescription(sdp, sdpType string) error {
	if pc.handle == nil {
		return errors.New("peer connection closed")
	}

	sdpC := C.CString(sdp)
	defer C.free(unsafe.Pointer(sdpC))
	typeC := C.CString(sdpType)
	defer C.free(unsafe.Pointer(typeC))

	if C.webrtc_objc_pc_set_local_description(pc.handle, sdpC, typeC) != 0 {
		return errors.New("failed to set local description")
	}
	return nil
}

// SetRemoteDescription sets the remote description
func (pc *PeerConnection) SetRemoteDescription(sdp, sdpType string) error {
	if pc.handle == nil {
		return errors.New("peer connection closed")
	}

	sdpC := C.CString(sdp)
	defer C.free(unsafe.Pointer(sdpC))
	typeC := C.CString(sdpType)
	defer C.free(unsafe.Pointer(typeC))

	if C.webrtc_objc_pc_set_remote_description(pc.handle, sdpC, typeC) != 0 {
		return errors.New("failed to set remote description")
	}
	return nil
}

// GetLocalDescription returns the current local description SDP
func (pc *PeerConnection) GetLocalDescription() (string, error) {
	if pc.handle == nil {
		return "", errors.New("peer connection closed")
	}

	sdpC := C.webrtc_objc_pc_get_local_description(pc.handle)
	if sdpC == nil {
		return "", errors.New("no local description")
	}
	defer C.webrtc_objc_free_string(sdpC)

	return C.GoString(sdpC), nil
}

// Close closes the peer connection
func (pc *PeerConnection) Close() {
	if pc.handle != nil {
		C.webrtc_objc_pc_close(pc.handle)
		pc.handle = nil
	}
	if pc.callbackID != 0 {
		unregisterCallbacks(pc.callbackID)
		pc.callbackID = 0
	}
}

// I420ToRGBA converts an I420 video frame to RGBA
func I420ToRGBA(frame *VideoFrame) ([]byte, error) {
	if frame == nil {
		return nil, errors.New("nil frame")
	}

	rgbaSize := frame.Width * frame.Height * 4
	rgba := make([]byte, rgbaSize)

	result := C.webrtc_objc_i420_to_rgba(
		(*C.uint8_t)(unsafe.Pointer(&frame.DataY[0])), C.int(frame.StrideY),
		(*C.uint8_t)(unsafe.Pointer(&frame.DataU[0])), C.int(frame.StrideU),
		(*C.uint8_t)(unsafe.Pointer(&frame.DataV[0])), C.int(frame.StrideV),
		(*C.uint8_t)(unsafe.Pointer(&rgba[0])), C.int(frame.Width*4),
		C.int(frame.Width), C.int(frame.Height),
	)

	if result != 0 {
		return nil, errors.New("I420 to RGBA conversion failed")
	}

	return rgba, nil
}
