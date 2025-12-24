package internal

import (
	"encoding/binary"
	"io"
	"strings"
	"sync"
)

// RawVideoStreamWriter はコンテナを付けずに動画のみ出力するライター
type RawVideoStreamWriter struct {
	writer           io.Writer
	codecType        string
	mu               sync.Mutex
	ivfHeaderWritten bool
	firstTimestamp   uint32
}

// NewRawVideoStreamWriter は新しいRawVideoStreamWriterを作成
func NewRawVideoStreamWriter(w io.Writer, codecType string) *RawVideoStreamWriter {
	return &RawVideoStreamWriter{
		writer:    w,
		codecType: strings.ToLower(codecType),
	}
}

// WriteVideoFrame はビデオフレームを書き込む
func (w *RawVideoStreamWriter) WriteVideoFrame(data []byte, timestamp uint32, keyframe bool) error {
	if len(data) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.codecType {
	case "vp8", "vp9":
		if !w.ivfHeaderWritten {
			fourCC := "VP80"
			if w.codecType == "vp9" {
				fourCC = "VP90"
			}
			if err := WriteIVFHeader(w.writer, fourCC); err != nil {
				return err
			}
			w.ivfHeaderWritten = true
			w.firstTimestamp = timestamp
		}

		frameHeader := make([]byte, 12)
		binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(data)))
		frameTimestamp := uint64(0)
		if timestamp >= w.firstTimestamp {
			frameTimestamp = uint64((timestamp - w.firstTimestamp) / 90)
		}
		binary.LittleEndian.PutUint64(frameHeader[4:], frameTimestamp)

		if _, err := w.writer.Write(frameHeader); err != nil {
			return err
		}
		_, err := w.writer.Write(data)
		return err
	default:
		_, err := w.writer.Write(data)
		return err
	}
}

// WriteAudioFrame はオーディオフレームを書き込む
func (w *RawVideoStreamWriter) WriteAudioFrame(data []byte, timestamp uint32) error {
	return nil
}

// Run はメインループを実行
func (w *RawVideoStreamWriter) Run() error {
	return nil
}

// Close はリソースをクリーンアップ
func (w *RawVideoStreamWriter) Close() error {
	return nil
}
