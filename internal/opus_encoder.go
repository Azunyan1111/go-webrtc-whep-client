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
	enc                 *opus.OpusEncoder
	sampleRate          int
	channels            int
	frameSize           int // samples per channel per frame (10ms = sampleRate * 10 / 1000)
	pcmBuffer           []byte
	bufferStartTSMs     int64 // timestamp of the first sample in pcmBuffer
	hasBufferStartTS    bool
	lastClusterTimeMs   int64
	hasLastClusterTime  bool
	encodedFrameCounter int64
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
		enc:                 enc,
		sampleRate:          sampleRate,
		channels:            channels,
		frameSize:           frameSize,
		pcmBuffer:           make([]byte, 0),
		bufferStartTSMs:     0,
		hasBufferStartTS:    false,
		lastClusterTimeMs:   0,
		hasLastClusterTime:  false,
		encodedFrameCounter: 0,
	}, nil
}

// Encode encodes PCM data to Opus frames with timestamps derived from MKV timing.
// クラスター時刻をアンカーとして、10msごとのOpusフレームにPTSを割り当てる。
func (e *OpusEncoder) Encode(pcm []byte, inputTimestampMs int64, clusterTimeMs int64) ([]EncodedAudioFrame, error) {
	if !e.hasBufferStartTS {
		e.bufferStartTSMs = inputTimestampMs
		e.hasBufferStartTS = true
		DebugLog("Opus encoder timestamp anchor initialized: input=%dms cluster=%dms\n", inputTimestampMs, clusterTimeMs)
	}

	if !e.hasLastClusterTime || clusterTimeMs != e.lastClusterTimeMs {
		// クラスター境界でアンカーを更新する。
		// 既にバッファがある場合は残サンプル分を逆算して先頭サンプル時刻を維持する。
		if len(e.pcmBuffer) == 0 {
			e.bufferStartTSMs = inputTimestampMs
		} else {
			bufferedSamples := int64(len(e.pcmBuffer) / (e.channels * 2))
			bufferedDurationMs := bufferedSamples * 1000 / int64(e.sampleRate)
			e.bufferStartTSMs = inputTimestampMs - bufferedDurationMs
		}
		e.lastClusterTimeMs = clusterTimeMs
		e.hasLastClusterTime = true
		DebugLog("Opus encoder cluster anchor updated: cluster=%dms input=%dms buffer_start=%dms\n",
			clusterTimeMs, inputTimestampMs, e.bufferStartTSMs)
	} else if len(e.pcmBuffer) == 0 {
		// バッファが空の場合は入力PTSをそのまま次のアンカーにする。
		e.bufferStartTSMs = inputTimestampMs
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
			// エンコード失敗時もサンプル消費分だけ時刻を進める。
			e.bufferStartTSMs += int64(e.frameSize * 1000 / e.sampleRate)
			e.encodedFrameCounter++
			continue
		}
		if n > 0 {
			frameTimestampMs := e.bufferStartTSMs
			encodedFrames = append(encodedFrames, EncodedAudioFrame{
				Data:        outBuf[:n],
				TimestampMs: frameTimestampMs,
			})
			// Log once per second (100 frames * 10ms = 1000ms)
			if e.encodedFrameCounter%100 == 0 {
				DebugLog("Opus frame encoded: timestamp=%dms, size=%d bytes, total frames=%d\n", frameTimestampMs, n, e.encodedFrameCounter)
			}
		}
		e.bufferStartTSMs += int64(e.frameSize * 1000 / e.sampleRate)
		e.encodedFrameCounter++
	}

	return encodedFrames, nil
}

func (e *OpusEncoder) Close() {
	if e.enc != nil {
		e.enc.Close()
		e.enc = nil
	}
}
