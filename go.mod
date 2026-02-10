module github.com/Azunyan1111/go-webrtc-whep-client

go 1.24.2

require (
	github.com/Azunyan1111/libvpx-go v0.6.2
	github.com/pion/interceptor v0.1.43
	github.com/pion/rtcp v1.2.16
	github.com/pion/rtp v1.10.0
	github.com/pion/webrtc/v4 v4.2.3
	github.com/qrtc/opus-go v0.0.1
	github.com/remko/go-mkvparse v0.14.0
	github.com/spf13/pflag v1.0.10
)

replace github.com/pion/interceptor => github.com/Azunyan1111/interceptor v0.0.0-20260126231723-d28190ee52d8

replace github.com/qrtc/opus-go => github.com/Azunyan1111/opus-go v0.0.2

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/pion/datachannel v1.6.0 // indirect
	github.com/pion/dtls/v3 v3.0.10 // indirect
	github.com/pion/ice/v4 v4.2.0 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.1.0 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/sctp v1.9.2 // indirect
	github.com/pion/sdp/v3 v3.0.17 // indirect
	github.com/pion/srtp/v3 v3.0.10 // indirect
	github.com/pion/stun/v3 v3.1.1 // indirect
	github.com/pion/transport/v4 v4.0.1 // indirect
	github.com/pion/turn/v4 v4.1.4 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	golang.org/x/crypto v0.47.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)
