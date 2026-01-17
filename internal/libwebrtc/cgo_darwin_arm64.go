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
