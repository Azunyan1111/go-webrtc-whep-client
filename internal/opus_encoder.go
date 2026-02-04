package internal

import (
	"fmt"

	opus "github.com/qrtc/opus-go"
)

// EncodedAudioFrame represents an encoded Opus frame with its timestamp
type EncodedAudioFrame struct {
	Data        []byte
	TimestampMs int64
}

type OpusEncoder struct {
	enc               *opus.OpusEncoder
	sampleRate        int
	channels          int
	frameSize         int // samples per channel per frame (10ms = sampleRate * 10 / 1000)
	pcmBuffer         []byte
	encodedFrameCount int64 // total number of encoded frames (for timestamp calculation)
	baseTimestampMs   int64 // base timestamp from first input
	initialized       bool  // whether base timestamp has been set
}

func NewOpusEncoder(sampleRate, channels int) (*OpusEncoder, error) {
	if sampleRate != 48000 {
		return nil, fmt.Errorf("only 48000Hz sample rate is supported, got %d", sampleRate)
	}
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("only 1 or 2 channels are supported, got %d", channels)
	}

	enc, err := opus.CreateOpusEncoder(&opus.OpusEncoderConfig{
		SampleRate:  sampleRate,
		MaxChannels: channels,
		Application: opus.AppAudio,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Opus encoder: %v", err)
	}

	// 10ms frame at 48000Hz
	frameSize := sampleRate * 10 / 1000

	DebugLog("Opus encoder initialized: %dHz, %d channels, frame size %d samples\n",
		sampleRate, channels, frameSize)

	return &OpusEncoder{
		enc:               enc,
		sampleRate:        sampleRate,
		channels:          channels,
		frameSize:         frameSize,
		pcmBuffer:         make([]byte, 0),
		encodedFrameCount: 0,
		baseTimestampMs:   0,
		initialized:       false,
	}, nil
}

// Encode encodes PCM data to Opus frames with proper timestamps.
// Each output frame is 10ms, and timestamps are calculated based on
// the number of frames encoded since initialization.
func (e *OpusEncoder) Encode(pcm []byte, inputTimestampMs int64) ([]EncodedAudioFrame, error) {
	// Initialize base timestamp on first call
	if !e.initialized {
		e.baseTimestampMs = inputTimestampMs
		e.initialized = true
		DebugLog("Opus encoder base timestamp initialized: %dms\n", e.baseTimestampMs)
	}

	e.pcmBuffer = append(e.pcmBuffer, pcm...)

	// PCM S16LE: 2 bytes per sample per channel
	bytesPerFrame := e.frameSize * e.channels * 2
	var encodedFrames []EncodedAudioFrame

	for len(e.pcmBuffer) >= bytesPerFrame {
		frameData := e.pcmBuffer[:bytesPerFrame]
		e.pcmBuffer = e.pcmBuffer[bytesPerFrame:]

		// Output buffer for encoded Opus data (max Opus frame size is ~1500 bytes)
		outBuf := make([]byte, 1500)
		n, err := e.enc.Encode(frameData, outBuf)
		if err != nil {
			DebugLog("Opus encode error: %v\n", err)
			// Even on error, increment frame count to maintain timing
			e.encodedFrameCount++
			continue
		}
		if n > 0 {
			// Calculate timestamp: base + (frame_count * 10ms)
			frameTimestampMs := e.baseTimestampMs + (e.encodedFrameCount * 10)
			encodedFrames = append(encodedFrames, EncodedAudioFrame{
				Data:        outBuf[:n],
				TimestampMs: frameTimestampMs,
			})
			// Log once per second (100 frames * 10ms = 1000ms)
			if e.encodedFrameCount%100 == 0 {
				DebugLog("Opus frame encoded: timestamp=%dms, size=%d bytes, total frames=%d\n", frameTimestampMs, n, e.encodedFrameCount)
			}
		}
		e.encodedFrameCount++
	}

	return encodedFrames, nil
}

func (e *OpusEncoder) Close() {
	if e.enc != nil {
		e.enc.Close()
		e.enc = nil
	}
}
