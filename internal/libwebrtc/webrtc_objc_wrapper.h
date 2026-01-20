// webrtc_objc_wrapper.h - Objective-C wrapper for libwebrtc
// This uses Objective-C to bridge between Go (CGO) and libwebrtc C++

#ifndef WEBRTC_OBJC_WRAPPER_H
#define WEBRTC_OBJC_WRAPPER_H

#ifdef __cplusplus
extern "C" {
#endif

#include <stdint.h>
#include <stdbool.h>

// Opaque handles
typedef void* WebRTCFactoryHandle;
typedef void* PeerConnectionHandle;

// ICE connection states
typedef enum {
    ICE_CONNECTION_NEW = 0,
    ICE_CONNECTION_CHECKING = 1,
    ICE_CONNECTION_CONNECTED = 2,
    ICE_CONNECTION_COMPLETED = 3,
    ICE_CONNECTION_FAILED = 4,
    ICE_CONNECTION_DISCONNECTED = 5,
    ICE_CONNECTION_CLOSED = 6
} ICEConnectionState;

// ICE gathering states
typedef enum {
    ICE_GATHERING_NEW = 0,
    ICE_GATHERING_GATHERING = 1,
    ICE_GATHERING_COMPLETE = 2
} ICEGatheringState;

// Callback types
typedef void (*OnICEStateCallback)(uintptr_t userData, int state);
typedef void (*OnICEGatheringStateCallback)(uintptr_t userData, int state);
typedef void (*OnVideoFrameCallback)(uintptr_t userData,
    const uint8_t* dataY, int strideY,
    const uint8_t* dataU, int strideU,
    const uint8_t* dataV, int strideV,
    int width, int height, int64_t timestamp_us);
typedef void (*OnEncodedAudioCallback)(uintptr_t userData,
    const uint8_t* data, int dataLen,
    uint32_t timestamp, uint16_t sequenceNumber);

// Factory functions
WebRTCFactoryHandle webrtc_objc_factory_create(void);
void webrtc_objc_factory_destroy(WebRTCFactoryHandle factory);

// PeerConnection functions
PeerConnectionHandle webrtc_objc_pc_create(
    WebRTCFactoryHandle factory,
    const char* stunServer,
    uintptr_t userData,
    OnICEStateCallback onICEState,
    OnICEGatheringStateCallback onICEGathering,
    OnVideoFrameCallback onVideoFrame,
    OnEncodedAudioCallback onEncodedAudio
);

int webrtc_objc_pc_add_video_transceiver(PeerConnectionHandle pc);
int webrtc_objc_pc_add_audio_transceiver(PeerConnectionHandle pc);

char* webrtc_objc_pc_create_offer(PeerConnectionHandle pc);
int webrtc_objc_pc_set_local_description(PeerConnectionHandle pc, const char* sdp, const char* type);
int webrtc_objc_pc_set_remote_description(PeerConnectionHandle pc, const char* sdp, const char* type);
char* webrtc_objc_pc_get_local_description(PeerConnectionHandle pc);

void webrtc_objc_pc_close(PeerConnectionHandle pc);

// Utility functions
void webrtc_objc_free_string(char* str);

// I420 to RGBA conversion using libyuv
int webrtc_objc_i420_to_rgba(
    const uint8_t* srcY, int srcStrideY,
    const uint8_t* srcU, int srcStrideU,
    const uint8_t* srcV, int srcStrideV,
    uint8_t* dstRGBA, int dstStrideRGBA,
    int width, int height
);

#ifdef __cplusplus
}
#endif

#endif // WEBRTC_OBJC_WRAPPER_H
