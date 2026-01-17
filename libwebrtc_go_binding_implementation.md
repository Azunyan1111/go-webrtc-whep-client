# libwebrtc Go バインディング実装ガイド

## 概要

このドキュメントは、Google libwebrtc (shiguredo-webrtc-build) を Go から CGO 経由で使用するためのバインディング実装方法を詳細に記述します。

## 前提条件

- macOS arm64 (Apple Silicon)
- Go 1.21以上
- Xcode Command Line Tools
- shiguredo-webrtc-build からダウンロードした libwebrtc

## 1. libwebrtc のダウンロード

shiguredo-webrtc-build から事前ビルド済みバイナリをダウンロードします。

```bash
# macOS arm64 用
curl -L -o webrtc-macos-arm64.tar.gz \
  https://github.com/AzuCLR/libwebrtc-build/releases/download/m120.0.6099.129/libwebrtc-macos-arm64-m120.0.6099.129.tar.gz

mkdir -p webrtc-macos-arm64
tar xzf webrtc-macos-arm64.tar.gz -C webrtc-macos-arm64
```

ディレクトリ構造:
```
webrtc-macos-arm64/
└── webrtc/
    ├── include/          # ヘッダーファイル
    │   ├── api/
    │   ├── rtc_base/
    │   ├── third_party/
    │   │   ├── abseil-cpp/
    │   │   └── libyuv/
    │   └── buildtools/
    │       └── third_party/
    │           └── libc++/
    │               └── __config_site  # 重要: ABI設定ファイル
    └── lib/
        └── libwebrtc.a   # スタティックライブラリ
```

## 2. ABI互換性問題と解決策

### 問題の説明

libwebrtc は Chromium の libc++ を使用しており、シンボルは `std::__Cr` 名前空間でマングリングされています。
一方、macOS のシステム libc++ は `std::__1` 名前空間を使用します。

```
# libwebrtc のシンボル例
__ZN6webrtc27CreatePeerConnectionFactory...NSt4__Cr10unique_ptr...

# システム libc++ でコンパイルした場合のシンボル
__ZN6webrtc27CreatePeerConnectionFactory...NSt3__110unique_ptr...
```

この違いにより、通常のコンパイルではリンクエラーが発生します。

### 解決策

Chromium の libc++ 設定ファイル (`__config_site`) をインクルードしてコンパイルすることで、
ラッパーコードも `std::__Cr` 名前空間でシンボルを生成します。

```bash
c++ -c wrapper.mm -o wrapper.o \
    -include path/to/webrtc/include/buildtools/third_party/libc++/__config_site \
    ...
```

## 3. ファイル構成

```
internal/libwebrtc/
├── webrtc_objc_wrapper.h      # C API ヘッダー
├── webrtc_objc_wrapper.mm     # Objective-C++ 実装
├── libwebrtc_objc_wrapper.a   # 事前コンパイル済みスタティックライブラリ
├── cgo_darwin_arm64.go        # CGO フラグ設定 (macOS arm64)
├── cgo_linux_amd64.go         # CGO フラグ設定 (Linux x86_64)
├── libwebrtc.go               # Go バインディング
└── callbacks.go               # Go コールバック関数
```

## 4. C API ヘッダー (webrtc_objc_wrapper.h)

```c
#ifndef WEBRTC_OBJC_WRAPPER_H
#define WEBRTC_OBJC_WRAPPER_H

#ifdef __cplusplus
extern "C" {
#endif

#include <stdint.h>
#include <stdbool.h>

// ハンドル型
typedef void* WebRTCFactoryHandle;
typedef void* PeerConnectionHandle;

// コールバック型 (uintptr_t を使用してGo側のポインタ安全性を確保)
typedef void (*OnICEStateCallback)(uintptr_t userData, int state);
typedef void (*OnICEGatheringStateCallback)(uintptr_t userData, int state);
typedef void (*OnVideoFrameCallback)(uintptr_t userData,
    const uint8_t* dataY, int strideY,
    const uint8_t* dataU, int strideU,
    const uint8_t* dataV, int strideV,
    int width, int height, uint32_t timestamp);
typedef void (*OnAudioDataCallback)(uintptr_t userData,
    const int16_t* data, int sampleRate,
    int channels, int frames, int64_t timestamp);

// Factory 関数
WebRTCFactoryHandle webrtc_objc_factory_create(void);
void webrtc_objc_factory_destroy(WebRTCFactoryHandle factory);

// PeerConnection 関数
PeerConnectionHandle webrtc_objc_pc_create(
    WebRTCFactoryHandle factory,
    const char* stunServer,
    uintptr_t userData,
    OnICEStateCallback onICEState,
    OnICEGatheringStateCallback onICEGathering,
    OnVideoFrameCallback onVideoFrame,
    OnAudioDataCallback onAudioData
);

int webrtc_objc_pc_add_video_transceiver(PeerConnectionHandle pc);
int webrtc_objc_pc_add_audio_transceiver(PeerConnectionHandle pc);
char* webrtc_objc_pc_create_offer(PeerConnectionHandle pc);
int webrtc_objc_pc_set_local_description(PeerConnectionHandle pc, const char* sdp, const char* type);
int webrtc_objc_pc_set_remote_description(PeerConnectionHandle pc, const char* sdp, const char* type);
char* webrtc_objc_pc_get_local_description(PeerConnectionHandle pc);
void webrtc_objc_pc_close(PeerConnectionHandle pc);

// ユーティリティ
void webrtc_objc_free_string(char* str);
int webrtc_objc_i420_to_rgba(...);

#ifdef __cplusplus
}
#endif
#endif
```

## 5. Objective-C++ 実装の要点 (webrtc_objc_wrapper.mm)

### 5.1 必要なインクルード

```cpp
#import <Foundation/Foundation.h>
#include "webrtc_objc_wrapper.h"

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
#include "api/make_ref_counted.h"
#include "rtc_base/thread.h"
#include "libyuv/convert_argb.h"
```

### 5.2 名前空間の注意点

libwebrtc M120以降では以下の変更があります:

```cpp
// 古い API (使用不可)
rtc::VideoSinkInterface
rtc::VideoSinkWants
rtc::make_ref_counted
cricket::MediaType::MEDIA_TYPE_VIDEO

// 新しい API (M120以降)
webrtc::VideoSinkInterface
webrtc::VideoSinkWants
webrtc::make_ref_counted
webrtc::MediaType::VIDEO
```

### 5.3 Factory クラス

```cpp
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
                nullptr,  // AudioDeviceModule
                webrtc::CreateBuiltinAudioEncoderFactory(),
                webrtc::CreateBuiltinAudioDecoderFactory(),
                webrtc::CreateBuiltinVideoEncoderFactory(),
                webrtc::CreateBuiltinVideoDecoderFactory(),
                nullptr,  // AudioMixer
                nullptr,  // AudioProcessing
                nullptr,  // AudioFrameProcessor
                nullptr   // FieldTrials
            );
        }
    }
    // ...
};
```

### 5.4 VideoSink 実装

```cpp
class VideoSinkImpl : public webrtc::VideoSinkInterface<webrtc::VideoFrame> {
public:
    VideoSinkImpl(uintptr_t userData, OnVideoFrameCallback callback)
        : userData_(userData), callback_(callback) {}

    void OnFrame(const webrtc::VideoFrame& frame) {
        if (!callback_) return;

        webrtc::scoped_refptr<webrtc::I420BufferInterface> i420_buffer =
            frame.video_frame_buffer()->ToI420();

        if (!i420_buffer) return;

        callback_(userData_,
            i420_buffer->DataY(), i420_buffer->StrideY(),
            i420_buffer->DataU(), i420_buffer->StrideU(),
            i420_buffer->DataV(), i420_buffer->StrideV(),
            i420_buffer->width(), i420_buffer->height(),
            timestamp);
    }

private:
    uintptr_t userData_;
    OnVideoFrameCallback callback_;
};
```

## 6. スタティックライブラリのビルド

### 6.1 ビルドコマンド

```bash
cd internal/libwebrtc

# Objective-C++ をコンパイル (重要: __config_site をインクルード)
c++ -c webrtc_objc_wrapper.mm -o webrtc_objc_wrapper.o \
    -std=c++17 -stdlib=libc++ \
    -include ../../webrtc-macos-arm64/webrtc/include/buildtools/third_party/libc++/__config_site \
    -I../../webrtc-macos-arm64/webrtc/include \
    -I../../webrtc-macos-arm64/webrtc/include/third_party/abseil-cpp \
    -I../../webrtc-macos-arm64/webrtc/include/third_party/libyuv/include \
    -DWEBRTC_MAC -DWEBRTC_POSIX

# スタティックライブラリを作成
ar rcs libwebrtc_objc_wrapper.a webrtc_objc_wrapper.o
```

### 6.2 シンボルの確認

```bash
# __Cr 名前空間であることを確認
nm webrtc_objc_wrapper.o | grep "CreatePeerConnectionFactory"
# 期待される出力: ...NSt4__Cr10unique_ptr...

# __1 が含まれていないことを確認
nm webrtc_objc_wrapper.o | grep " U " | grep "__1" | wc -l
# 期待される出力: 0
```

## 7. CGO 設定 (cgo_darwin_arm64.go)

```go
//go:build darwin && arm64

package libwebrtc

/*
#cgo CXXFLAGS: -std=c++17 -stdlib=libc++
#cgo CXXFLAGS: -I${SRCDIR}/../../webrtc-macos-arm64/webrtc/include
#cgo CXXFLAGS: -I${SRCDIR}/../../webrtc-macos-arm64/webrtc/include/third_party/abseil-cpp
#cgo CXXFLAGS: -I${SRCDIR}/../../webrtc-macos-arm64/webrtc/include/third_party/libyuv/include
#cgo CXXFLAGS: -DWEBRTC_MAC -DWEBRTC_POSIX

#cgo LDFLAGS: -L${SRCDIR} -lwebrtc_objc_wrapper
#cgo LDFLAGS: -L${SRCDIR}/../../webrtc-macos-arm64/webrtc/lib -lwebrtc
#cgo LDFLAGS: -lc++
#cgo LDFLAGS: -framework Foundation
#cgo LDFLAGS: -framework AVFoundation
#cgo LDFLAGS: -framework CoreAudio
#cgo LDFLAGS: -framework AudioToolbox
#cgo LDFLAGS: -framework CoreMedia
#cgo LDFLAGS: -framework CoreVideo
#cgo LDFLAGS: -framework VideoToolbox
#cgo LDFLAGS: -framework Metal
#cgo LDFLAGS: -framework MetalKit
#cgo LDFLAGS: -framework IOKit
#cgo LDFLAGS: -framework IOSurface
#cgo LDFLAGS: -framework CoreFoundation
#cgo LDFLAGS: -framework CoreGraphics
#cgo LDFLAGS: -framework CoreServices
#cgo LDFLAGS: -framework ApplicationServices
#cgo LDFLAGS: -framework Security
#cgo LDFLAGS: -framework SystemConfiguration
#cgo LDFLAGS: -framework OpenGL
#cgo LDFLAGS: -framework AppKit

#include "webrtc_objc_wrapper.h"
*/
import "C"
```

**重要**: リンク順序は `libwebrtc_objc_wrapper.a` が先、`libwebrtc.a` が後です。

## 8. Go コールバック (callbacks.go)

```go
package libwebrtc

/*
#include <stdint.h>
*/
import "C"
import "unsafe"

//export goOnICEState
func goOnICEState(userData C.uintptr_t, state C.int) {
    id := uintptr(userData)
    if cb := getCallbacks(id); cb != nil && cb.OnICEConnectionState != nil {
        cb.OnICEConnectionState(ICEConnectionState(state))
    }
}

//export goOnVideoFrame
func goOnVideoFrame(userData C.uintptr_t,
    dataY *C.uint8_t, strideY C.int,
    dataU *C.uint8_t, strideU C.int,
    dataV *C.uint8_t, strideV C.int,
    width, height C.int, timestamp C.uint32_t) {

    id := uintptr(userData)
    cb := getCallbacks(id)
    if cb == nil || cb.OnVideoFrame == nil {
        return
    }

    // I420 データを Go スライスにコピー
    frame := &VideoFrame{
        DataY:     C.GoBytes(unsafe.Pointer(dataY), C.int(sizeY)),
        DataU:     C.GoBytes(unsafe.Pointer(dataU), C.int(sizeU)),
        DataV:     C.GoBytes(unsafe.Pointer(dataV), C.int(sizeV)),
        // ...
    }
    cb.OnVideoFrame(frame)
}
```

**重要**: `void*` ではなく `uintptr_t` / `C.uintptr_t` を使用して `go vet` 警告を回避します。

## 9. ビルドと実行

```bash
# ビルド
go build -o whep-libwebrtc-go ./cmd/whep-libwebrtc-go

# 実行
./whep-libwebrtc-go https://example.com/whep | ffplay -i -
```

## 10. トラブルシューティング

### 10.1 リンクエラー: Undefined symbols std::__1

**原因**: ラッパーが `__config_site` なしでコンパイルされた

**解決策**: コンパイル時に `-include .../__config_site` を追加

### 10.2 実行時エラー: dyld: Library not loaded

**原因**: 必要な Framework がリンクされていない

**解決策**: CGO LDFLAGS に必要な `-framework` を追加

### 10.3 go vet 警告: possible misuse of unsafe.Pointer

**原因**: `unsafe.Pointer(uintptr)` の変換が危険と判断された

**解決策**: C API を `void*` から `uintptr_t` に変更

## 11. 対応プラットフォーム

| プラットフォーム | 状態 | 備考 |
|----------------|------|------|
| macOS arm64 | 動作確認済み | M1/M2/M3 Mac |
| macOS x86_64 | 未テスト | 同様の手順で可能 |
| Linux x86_64 | 未テスト | Framework を除去、pthread 追加 |
| Windows | 未対応 | MSVC ABI 対応が必要 |

## 12. 参考資料

- shiguredo-webrtc-build: https://github.com/AzuCLR/libwebrtc-build
- WebRTC Native Code: https://webrtc.googlesource.com/src/
- CGO documentation: https://pkg.go.dev/cmd/cgo
