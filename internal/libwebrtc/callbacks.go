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

//export goOnEncodedAudio
func goOnEncodedAudio(userData C.uintptr_t,
	data *C.uint8_t, dataLen C.int,
	timestamp C.uint32_t, sequenceNumber C.uint16_t) {

	id := uintptr(userData)
	cb := getCallbacks(id)
	if cb == nil || cb.OnEncodedAudioFrame == nil {
		return
	}

	frame := &EncodedAudioFrame{
		Data:           C.GoBytes(unsafe.Pointer(data), dataLen),
		Timestamp:      uint32(timestamp),
		SequenceNumber: uint16(sequenceNumber),
	}

	cb.OnEncodedAudioFrame(frame)
}
