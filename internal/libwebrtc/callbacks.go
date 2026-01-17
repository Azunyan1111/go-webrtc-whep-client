package libwebrtc

/*
#include <stdint.h>
*/
import "C"
import "unsafe"

// These functions are called from C code

//export goOnICEState
func goOnICEState(userData C.uintptr_t, state C.int) {
	id := uintptr(userData)
	if cb := getCallbacks(id); cb != nil && cb.OnICEConnectionState != nil {
		cb.OnICEConnectionState(ICEConnectionState(state))
	}
}

//export goOnICEGathering
func goOnICEGathering(userData C.uintptr_t, state C.int) {
	id := uintptr(userData)
	if cb := getCallbacks(id); cb != nil && cb.OnICEGatheringState != nil {
		cb.OnICEGatheringState(ICEGatheringState(state))
	}
}

//export goOnVideoFrame
func goOnVideoFrame(userData C.uintptr_t,
	dataY *C.uint8_t, strideY C.int,
	dataU *C.uint8_t, strideU C.int,
	dataV *C.uint8_t, strideV C.int,
	width, height C.int, timestampUs C.int64_t) {

	id := uintptr(userData)
	cb := getCallbacks(id)
	if cb == nil || cb.OnVideoFrame == nil {
		return
	}

	w := int(width)
	h := int(height)
	sY := int(strideY)
	sU := int(strideU)
	sV := int(strideV)

	// Calculate buffer sizes
	sizeY := sY * h
	sizeU := sU * (h / 2)
	sizeV := sV * (h / 2)

	// Copy data to Go slices
	frame := &VideoFrame{
		DataY:       C.GoBytes(unsafe.Pointer(dataY), C.int(sizeY)),
		DataU:       C.GoBytes(unsafe.Pointer(dataU), C.int(sizeU)),
		DataV:       C.GoBytes(unsafe.Pointer(dataV), C.int(sizeV)),
		StrideY:     sY,
		StrideU:     sU,
		StrideV:     sV,
		Width:       w,
		Height:      h,
		TimestampUs: int64(timestampUs),
	}

	cb.OnVideoFrame(frame)
}

//export goOnAudioData
func goOnAudioData(userData C.uintptr_t,
	data *C.int16_t, sampleRate C.int,
	channels C.int, frames C.int, timestampUs C.int64_t) {

	id := uintptr(userData)
	cb := getCallbacks(id)
	if cb == nil || cb.OnAudioFrame == nil {
		return
	}

	numSamples := int(channels) * int(frames)

	// Copy PCM data to Go slice
	pcmSlice := make([]int16, numSamples)
	src := unsafe.Slice((*int16)(unsafe.Pointer(data)), numSamples)
	copy(pcmSlice, src)

	frame := &AudioFrame{
		PCM:         pcmSlice,
		SampleRate:  int(sampleRate),
		Channels:    int(channels),
		Frames:      int(frames),
		TimestampUs: int64(timestampUs),
	}

	cb.OnAudioFrame(frame)
}
