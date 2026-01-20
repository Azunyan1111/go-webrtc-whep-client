// webrtc_objc_wrapper.mm - Objective-C++ implementation of libwebrtc wrapper
// Using Objective-C++ to handle C++ ABI compatibility issues

#import <Foundation/Foundation.h>
#include "webrtc_objc_wrapper.h"

#include <memory>
#include <string>
#include <mutex>
#include <condition_variable>
#include <cstring>
#include <optional>

#include "api/peer_connection_interface.h"
#include "api/create_peerconnection_factory.h"
#include "api/audio_codecs/builtin_audio_decoder_factory.h"
#include "api/audio_codecs/builtin_audio_encoder_factory.h"
#include "api/video_codecs/builtin_video_decoder_factory.h"
#include "api/video_codecs/builtin_video_encoder_factory.h"
#include "api/video/video_frame.h"
#include "api/video/video_sink_interface.h"
#include "api/video/i420_buffer.h"
#include "api/media_stream_interface.h"
#include "api/media_types.h"
#include "api/rtc_event_log/rtc_event_log_factory.h"
#include "api/task_queue/default_task_queue_factory.h"
#include "api/set_local_description_observer_interface.h"
#include "api/set_remote_description_observer_interface.h"
#include "api/make_ref_counted.h"
#include "rtc_base/thread.h"
#include "rtc_base/ref_counted_object.h"
#include "libyuv/convert_argb.h"
#include "api/frame_transformer_interface.h"

namespace {

// WebRTC Factory wrapper
class WebRTCFactory {
public:
    WebRTCFactory() {
        @autoreleasepool {
            network_thread_ = webrtc::Thread::CreateWithSocketServer();
            network_thread_->SetName("network_thread", nullptr);
            network_thread_->Start();

            worker_thread_ = webrtc::Thread::Create();
            worker_thread_->SetName("worker_thread", nullptr);
            worker_thread_->Start();

            signaling_thread_ = webrtc::Thread::Create();
            signaling_thread_->SetName("signaling_thread", nullptr);
            signaling_thread_->Start();

            factory_ = webrtc::CreatePeerConnectionFactory(
                network_thread_.get(),
                worker_thread_.get(),
                signaling_thread_.get(),
                nullptr,  // default ADM
                webrtc::CreateBuiltinAudioEncoderFactory(),
                webrtc::CreateBuiltinAudioDecoderFactory(),
                webrtc::CreateBuiltinVideoEncoderFactory(),
                webrtc::CreateBuiltinVideoDecoderFactory(),
                nullptr,  // audio mixer
                nullptr,  // audio processing
                nullptr,  // audio frame processor
                nullptr   // field trials
            );
        }
    }

    ~WebRTCFactory() {
        factory_ = nullptr;
        signaling_thread_->Stop();
        worker_thread_->Stop();
        network_thread_->Stop();
    }

    webrtc::PeerConnectionFactoryInterface* get() { return factory_.get(); }
    webrtc::Thread* signaling_thread() { return signaling_thread_.get(); }

private:
    std::unique_ptr<webrtc::Thread> network_thread_;
    std::unique_ptr<webrtc::Thread> worker_thread_;
    std::unique_ptr<webrtc::Thread> signaling_thread_;
    webrtc::scoped_refptr<webrtc::PeerConnectionFactoryInterface> factory_;
};

// Video sink implementation
class VideoSinkImpl : public webrtc::VideoSinkInterface<webrtc::VideoFrame> {
public:
    VideoSinkImpl(uintptr_t userData, OnVideoFrameCallback callback)
        : userData_(userData), callback_(callback) {}

    void OnFrame(const webrtc::VideoFrame& frame) {
        if (!callback_) return;

        webrtc::scoped_refptr<webrtc::I420BufferInterface> i420_buffer =
            frame.video_frame_buffer()->ToI420();

        if (!i420_buffer) return;

        // Pass timestamp in microseconds directly
        int64_t timestamp_us = frame.timestamp_us();

        callback_(userData_,
            i420_buffer->DataY(), i420_buffer->StrideY(),
            i420_buffer->DataU(), i420_buffer->StrideU(),
            i420_buffer->DataV(), i420_buffer->StrideV(),
            i420_buffer->width(), i420_buffer->height(),
            timestamp_us);
    }

private:
    uintptr_t userData_;
    OnVideoFrameCallback callback_;
};

// Audio frame transformer implementation for encoded Opus passthrough
class AudioFrameTransformer : public webrtc::FrameTransformerInterface {
public:
    AudioFrameTransformer(uintptr_t userData, OnEncodedAudioCallback callback)
        : userData_(userData), callback_(callback) {}

    void Transform(std::unique_ptr<webrtc::TransformableFrameInterface> frame) override {
        if (callback_) {
            auto* audio_frame = static_cast<webrtc::TransformableAudioFrameInterface*>(frame.get());
            auto data = frame->GetData();
            auto seqNum = audio_frame->SequenceNumber();

            // Debug: log first few bytes and size (only first 50 frames)
            static int debug_count = 0;
            if (debug_count < 50) {
                fprintf(stderr, "[AudioFrameTransformer] Frame size=%zu, timestamp=%u, seq=%u, first bytes:",
                        data.size(), frame->GetTimestamp(), seqNum.value_or(0));
                for (size_t i = 0; i < std::min(data.size(), size_t(16)); i++) {
                    fprintf(stderr, " %02x", data.data()[i]);
                }
                fprintf(stderr, "\n");
                fflush(stderr);
                debug_count++;
            }

            callback_(userData_,
                data.data(), static_cast<int>(data.size()),
                frame->GetTimestamp(),
                seqNum.value_or(0));
        }
        // Do not forward to decoder - skip PCM decoding
    }

    void RegisterTransformedFrameSinkCallback(
        webrtc::scoped_refptr<webrtc::TransformedFrameCallback> callback,
        uint32_t ssrc) override {
        // Not used - we don't forward frames to decoder
    }

    void UnregisterTransformedFrameSinkCallback(uint32_t ssrc) override {
        // Not used
    }

private:
    uintptr_t userData_;
    OnEncodedAudioCallback callback_;
};

// Create SDP Observer
class CreateSDPObserver : public webrtc::CreateSessionDescriptionObserver {
public:
    CreateSDPObserver() : done_(false), success_(false) {}

    void OnSuccess(webrtc::SessionDescriptionInterface* desc) override {
        std::lock_guard<std::mutex> lock(mutex_);
        desc->ToString(&sdp_);
        success_ = true;
        done_ = true;
        cv_.notify_one();
    }

    void OnFailure(webrtc::RTCError error) override {
        std::lock_guard<std::mutex> lock(mutex_);
        success_ = false;
        done_ = true;
        cv_.notify_one();
    }

    bool Wait(int timeout_ms = 5000) {
        std::unique_lock<std::mutex> lock(mutex_);
        return cv_.wait_for(lock, std::chrono::milliseconds(timeout_ms),
                           [this] { return done_; });
    }

    bool success() const { return success_; }
    const std::string& sdp() const { return sdp_; }

private:
    std::mutex mutex_;
    std::condition_variable cv_;
    bool done_;
    bool success_;
    std::string sdp_;
};

// Set Local Description Observer
class SetLocalSDPObserver : public webrtc::SetLocalDescriptionObserverInterface {
public:
    SetLocalSDPObserver() : done_(false), success_(false) {}

    void OnSetLocalDescriptionComplete(webrtc::RTCError error) override {
        std::lock_guard<std::mutex> lock(mutex_);
        success_ = error.ok();
        done_ = true;
        cv_.notify_one();
    }

    bool Wait(int timeout_ms = 5000) {
        std::unique_lock<std::mutex> lock(mutex_);
        return cv_.wait_for(lock, std::chrono::milliseconds(timeout_ms),
                           [this] { return done_; });
    }

    bool success() const { return success_; }

private:
    std::mutex mutex_;
    std::condition_variable cv_;
    bool done_;
    bool success_;
};

// Set Remote Description Observer
class SetRemoteSDPObserver : public webrtc::SetRemoteDescriptionObserverInterface {
public:
    SetRemoteSDPObserver() : done_(false), success_(false) {}

    void OnSetRemoteDescriptionComplete(webrtc::RTCError error) override {
        std::lock_guard<std::mutex> lock(mutex_);
        success_ = error.ok();
        done_ = true;
        cv_.notify_one();
    }

    bool Wait(int timeout_ms = 5000) {
        std::unique_lock<std::mutex> lock(mutex_);
        return cv_.wait_for(lock, std::chrono::milliseconds(timeout_ms),
                           [this] { return done_; });
    }

    bool success() const { return success_; }

private:
    std::mutex mutex_;
    std::condition_variable cv_;
    bool done_;
    bool success_;
};

// PeerConnection wrapper
class PeerConnectionWrapper : public webrtc::PeerConnectionObserver {
public:
    PeerConnectionWrapper(
        WebRTCFactory* factory,
        const char* stunServer,
        uintptr_t userData,
        OnICEStateCallback onICEState,
        OnICEGatheringStateCallback onICEGathering,
        OnVideoFrameCallback onVideoFrame,
        OnEncodedAudioCallback onEncodedAudio)
        : factory_(factory)
        , userData_(userData)
        , onICEState_(onICEState)
        , onICEGathering_(onICEGathering)
        , videoSink_(new VideoSinkImpl(userData, onVideoFrame))
        , audioFrameTransformer_(webrtc::make_ref_counted<AudioFrameTransformer>(userData, onEncodedAudio)) {

        webrtc::PeerConnectionInterface::RTCConfiguration config;
        config.sdp_semantics = webrtc::SdpSemantics::kUnifiedPlan;

        if (stunServer && strlen(stunServer) > 0) {
            webrtc::PeerConnectionInterface::IceServer ice_server;
            ice_server.uri = stunServer;
            config.servers.push_back(ice_server);
        }

        webrtc::PeerConnectionDependencies deps(this);
        auto result = factory->get()->CreatePeerConnectionOrError(config, std::move(deps));
        if (result.ok()) {
            pc_ = result.MoveValue();
        }
    }

    ~PeerConnectionWrapper() {
        if (pc_) {
            pc_->Close();
        }
    }

    webrtc::PeerConnectionInterface* pc() { return pc_.get(); }
    bool valid() const { return pc_ != nullptr; }

    // PeerConnectionObserver implementation
    void OnSignalingChange(webrtc::PeerConnectionInterface::SignalingState new_state) override {}

    void OnDataChannel(webrtc::scoped_refptr<webrtc::DataChannelInterface> data_channel) override {}

    void OnIceGatheringChange(webrtc::PeerConnectionInterface::IceGatheringState new_state) override {
        if (onICEGathering_) {
            onICEGathering_(userData_, static_cast<int>(new_state));
        }
    }

    void OnIceCandidate(const webrtc::IceCandidateInterface* candidate) override {}

    void OnIceConnectionChange(webrtc::PeerConnectionInterface::IceConnectionState new_state) override {
        if (onICEState_) {
            onICEState_(userData_, static_cast<int>(new_state));
        }
    }

    void OnTrack(webrtc::scoped_refptr<webrtc::RtpTransceiverInterface> transceiver) override {
        auto track = transceiver->receiver()->track();
        if (!track) return;

        NSLog(@"[OnTrack] Track kind: %s", track->kind().c_str());

        if (track->kind() == webrtc::MediaStreamTrackInterface::kVideoKind) {
            auto video_track = static_cast<webrtc::VideoTrackInterface*>(track.get());
            webrtc::VideoSinkWants wants;
            video_track->AddOrUpdateSink(videoSink_.get(), wants);
            NSLog(@"[OnTrack] Video sink added");
        } else if (track->kind() == webrtc::MediaStreamTrackInterface::kAudioKind) {
            // Use FrameTransformer to get encoded Opus frames
            NSLog(@"[OnTrack] Setting audio FrameTransformer");
            transceiver->receiver()->SetFrameTransformer(audioFrameTransformer_);
            NSLog(@"[OnTrack] Audio FrameTransformer set");
        }
    }

private:
    WebRTCFactory* factory_;
    webrtc::scoped_refptr<webrtc::PeerConnectionInterface> pc_;
    uintptr_t userData_;
    OnICEStateCallback onICEState_;
    OnICEGatheringStateCallback onICEGathering_;
    std::unique_ptr<VideoSinkImpl> videoSink_;
    webrtc::scoped_refptr<AudioFrameTransformer> audioFrameTransformer_;
};

}  // namespace

// C API implementation
extern "C" {

WebRTCFactoryHandle webrtc_objc_factory_create(void) {
    @autoreleasepool {
        auto* factory = new WebRTCFactory();
        if (factory->get() == nullptr) {
            delete factory;
            return nullptr;
        }
        return factory;
    }
}

void webrtc_objc_factory_destroy(WebRTCFactoryHandle factory) {
    if (factory) {
        delete static_cast<WebRTCFactory*>(factory);
    }
}

PeerConnectionHandle webrtc_objc_pc_create(
    WebRTCFactoryHandle factory,
    const char* stunServer,
    uintptr_t userData,
    OnICEStateCallback onICEState,
    OnICEGatheringStateCallback onICEGathering,
    OnVideoFrameCallback onVideoFrame,
    OnEncodedAudioCallback onEncodedAudio) {

    @autoreleasepool {
        if (!factory) return nullptr;

        auto* wrapper = new PeerConnectionWrapper(
            static_cast<WebRTCFactory*>(factory),
            stunServer,
            userData,
            onICEState,
            onICEGathering,
            onVideoFrame,
            onEncodedAudio
        );

        if (!wrapper->valid()) {
            delete wrapper;
            return nullptr;
        }

        return wrapper;
    }
}

int webrtc_objc_pc_add_video_transceiver(PeerConnectionHandle pc) {
    @autoreleasepool {
        if (!pc) return -1;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        webrtc::RtpTransceiverInit init;
        init.direction = webrtc::RtpTransceiverDirection::kRecvOnly;

        auto result = wrapper->pc()->AddTransceiver(webrtc::MediaType::VIDEO, init);
        return result.ok() ? 0 : -1;
    }
}

int webrtc_objc_pc_add_audio_transceiver(PeerConnectionHandle pc) {
    @autoreleasepool {
        if (!pc) return -1;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        webrtc::RtpTransceiverInit init;
        init.direction = webrtc::RtpTransceiverDirection::kRecvOnly;

        auto result = wrapper->pc()->AddTransceiver(webrtc::MediaType::AUDIO, init);
        return result.ok() ? 0 : -1;
    }
}

char* webrtc_objc_pc_create_offer(PeerConnectionHandle pc) {
    @autoreleasepool {
        if (!pc) return nullptr;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        auto observer = webrtc::make_ref_counted<CreateSDPObserver>();

        webrtc::PeerConnectionInterface::RTCOfferAnswerOptions options;
        options.offer_to_receive_video = true;
        options.offer_to_receive_audio = true;

        wrapper->pc()->CreateOffer(observer.get(), options);

        if (!observer->Wait() || !observer->success()) {
            return nullptr;
        }

        return strdup(observer->sdp().c_str());
    }
}

int webrtc_objc_pc_set_local_description(PeerConnectionHandle pc, const char* sdp, const char* type) {
    @autoreleasepool {
        if (!pc || !sdp || !type) return -1;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        webrtc::SdpType sdp_type;
        if (strcmp(type, "offer") == 0) {
            sdp_type = webrtc::SdpType::kOffer;
        } else if (strcmp(type, "answer") == 0) {
            sdp_type = webrtc::SdpType::kAnswer;
        } else {
            return -1;
        }

        webrtc::SdpParseError error;
        auto desc = webrtc::CreateSessionDescription(sdp_type, std::string(sdp), &error);
        if (!desc) {
            return -1;
        }

        auto observer = webrtc::make_ref_counted<SetLocalSDPObserver>();
        wrapper->pc()->SetLocalDescription(std::move(desc), observer);

        if (!observer->Wait() || !observer->success()) {
            return -1;
        }

        return 0;
    }
}

int webrtc_objc_pc_set_remote_description(PeerConnectionHandle pc, const char* sdp, const char* type) {
    @autoreleasepool {
        if (!pc || !sdp || !type) return -1;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        webrtc::SdpType sdp_type;
        if (strcmp(type, "offer") == 0) {
            sdp_type = webrtc::SdpType::kOffer;
        } else if (strcmp(type, "answer") == 0) {
            sdp_type = webrtc::SdpType::kAnswer;
        } else {
            return -1;
        }

        webrtc::SdpParseError error;
        auto desc = webrtc::CreateSessionDescription(sdp_type, std::string(sdp), &error);
        if (!desc) {
            return -1;
        }

        auto observer = webrtc::make_ref_counted<SetRemoteSDPObserver>();
        wrapper->pc()->SetRemoteDescription(std::move(desc), observer);

        if (!observer->Wait() || !observer->success()) {
            return -1;
        }

        return 0;
    }
}

char* webrtc_objc_pc_get_local_description(PeerConnectionHandle pc) {
    @autoreleasepool {
        if (!pc) return nullptr;
        auto* wrapper = static_cast<PeerConnectionWrapper*>(pc);

        auto desc = wrapper->pc()->local_description();
        if (!desc) return nullptr;

        std::string sdp;
        desc->ToString(&sdp);
        return strdup(sdp.c_str());
    }
}

void webrtc_objc_pc_close(PeerConnectionHandle pc) {
    if (pc) {
        delete static_cast<PeerConnectionWrapper*>(pc);
    }
}

void webrtc_objc_free_string(char* str) {
    if (str) {
        free(str);
    }
}

int webrtc_objc_i420_to_rgba(
    const uint8_t* srcY, int srcStrideY,
    const uint8_t* srcU, int srcStrideU,
    const uint8_t* srcV, int srcStrideV,
    uint8_t* dstRGBA, int dstStrideRGBA,
    int width, int height) {

    return libyuv::I420ToABGR(
        srcY, srcStrideY,
        srcU, srcStrideU,
        srcV, srcStrideV,
        dstRGBA, dstStrideRGBA,
        width, height
    );
}

}  // extern "C"
