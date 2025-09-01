package internal

import (
	"encoding/binary"
	"fmt"
	"io"
)

// RawStreamWriter は生のコーデックデータを出力するライター
type RawStreamWriter struct {
	writer           io.Writer
	codecType        string
	ivfHeaderWritten bool
	firstTimestamp   uint32
	frameCount       uint32
	oggHeaderWritten bool
	granulePosition  uint64
	pageSequence     uint32
	serialNumber     uint32
}

// NewRawStreamWriter は新しいRawStreamWriterを作成
func NewRawStreamWriter(w io.Writer, codecType string) *RawStreamWriter {
	return &RawStreamWriter{
		writer:       w,
		codecType:    codecType,
		serialNumber: 0x12345678,
	}
}

// WriteVideoFrame はビデオフレームを書き込む
func (r *RawStreamWriter) WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error {
	switch r.codecType {
	case "h264":
		// H264はスタートコード付きで出力
		startCode := []byte{0x00, 0x00, 0x00, 0x01}
		if _, err := r.writer.Write(startCode); err != nil {
			return fmt.Errorf("error writing start code: %w", err)
		}
		if _, err := r.writer.Write(data); err != nil {
			return fmt.Errorf("error writing NAL unit: %w", err)
		}

	case "vp8", "vp9":
		// VP8/VP9はIVFコンテナで出力
		if !r.ivfHeaderWritten {
			codecFourCC := "VP80"
			if r.codecType == "vp9" {
				codecFourCC = "VP90"
			}
			if err := WriteIVFHeader(r.writer, codecFourCC); err != nil {
				return fmt.Errorf("error writing IVF header: %w", err)
			}
			r.ivfHeaderWritten = true
			r.firstTimestamp = timestamp
		}

		// IVFフレームヘッダーを書き込み
		frameHeader := make([]byte, 12)
		binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(data)))
		// タイムスタンプ計算
		ts := (timestamp - r.firstTimestamp) / 90 // 90kHz to ms
		binary.LittleEndian.PutUint64(frameHeader[4:], uint64(ts))

		if _, err := r.writer.Write(frameHeader); err != nil {
			return fmt.Errorf("error writing IVF frame header: %w", err)
		}
		if _, err := r.writer.Write(data); err != nil {
			return fmt.Errorf("error writing frame data: %w", err)
		}
		r.frameCount++

	default:
		// その他のコーデックはそのまま出力
		if _, err := r.writer.Write(data); err != nil {
			return fmt.Errorf("error writing video data: %w", err)
		}
	}

	return nil
}

// WriteAudioFrame はオーディオフレームを書き込む
func (r *RawStreamWriter) WriteAudioFrame(data []byte, timestamp uint32) error {
	// OggOpusコンテナで出力
	if !r.oggHeaderWritten {
		seq, err := WriteOggOpusHeader(r.writer, r.serialNumber)
		if err != nil {
			return fmt.Errorf("error writing OggOpus header: %w", err)
		}
		r.pageSequence = seq
		r.oggHeaderWritten = true
	}

	// Opusパケットのサンプル数計算（20ms @ 48kHz）
	samplesPerPacket := uint64(960)
	r.granulePosition += samplesPerPacket

	// Oggページとして書き込み
	if err := WriteOggPage(r.writer, data, r.granulePosition, false, false, false, r.serialNumber, &r.pageSequence); err != nil {
		return fmt.Errorf("error writing Ogg page: %w", err)
	}

	return nil
}

// Run は必要ないので何もしない
func (r *RawStreamWriter) Run() error {
	return nil
}

// Close はリソースをクリーンアップ
func (r *RawStreamWriter) Close() error {
	if flusher, ok := r.writer.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}
