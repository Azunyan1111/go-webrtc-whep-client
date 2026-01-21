package libwebrtc

/*
#include <stdint.h>
*/
import "C"
import (
	"sync/atomic"
	"unsafe"
)

var audioCallbackCount uint64

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
	if cb == nil {
		DebugLog("[AUDIO][Go] callbacks not found: id=%d\n", id)
		return
	}
	if cb.OnAudioFrame == nil {
		DebugLog("[AUDIO][Go] OnAudioFrame is nil: id=%d\n", id)
		return
	}

	count := atomic.AddUint64(&audioCallbackCount, 1)

	// Calculate total samples
	numSamples := int(frames) * int(channels)

	// Copy PCM data to Go slice
	pcmData := make([]int16, numSamples)
	src := (*[1 << 28]C.int16_t)(unsafe.Pointer(data))[:numSamples:numSamples]
	for i := 0; i < numSamples; i++ {
		pcmData[i] = int16(src[i])
	}

	if count <= 3 || count%100 == 0 {
		DebugLog("[AUDIO][Go] onAudioData: count=%d rate=%dHz channels=%d frames=%d samples=%d ts_us=%d\n",
			count, int(sampleRate), int(channels), int(frames), numSamples, int64(timestampUs))
	}

	frame := &AudioFrame{
		PCM:         pcmData,
		SampleRate:  int(sampleRate),
		Channels:    int(channels),
		Frames:      int(frames),
		TimestampUs: int64(timestampUs),
	}

	cb.OnAudioFrame(frame)
}
