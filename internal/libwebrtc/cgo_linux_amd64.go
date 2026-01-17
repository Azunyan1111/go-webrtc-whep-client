//go:build linux && amd64

package libwebrtc

/*
#cgo CXXFLAGS: -std=c++17
#cgo CXXFLAGS: -I${SRCDIR}/../../webrtc-ubuntu-x86_64/webrtc/include
#cgo CXXFLAGS: -I${SRCDIR}/../../webrtc-ubuntu-x86_64/webrtc/include/third_party/abseil-cpp
#cgo CXXFLAGS: -DWEBRTC_LINUX -DWEBRTC_POSIX

#cgo LDFLAGS: -L${SRCDIR}/../../webrtc-ubuntu-x86_64/webrtc/lib -lwebrtc
#cgo LDFLAGS: -lstdc++ -lpthread -ldl -lm

#include "webrtc_wrapper.h"
*/
import "C"
