package internal

import (
	"fmt"
	"io"
	"sync"

	"github.com/pion/webrtc/v4"
)

// H264DirectStreamProcessor はH264ストリームを直接処理するプロセッサー
// 古い実装と同じように、NALユニットを即座に書き込む
type H264DirectStreamProcessor struct {
	videoTrack *webrtc.TrackRemote
	writer     io.Writer
	nalBuffer  []byte
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewH264DirectStreamProcessor は新しいH264ダイレクトストリームプロセッサーを作成
func NewH264DirectStreamProcessor(track *webrtc.TrackRemote, w io.Writer) *H264DirectStreamProcessor {
	return &H264DirectStreamProcessor{
		videoTrack: track,
		writer:     w,
		done:       make(chan struct{}),
	}
}

// Run はストリーム処理を開始
func (h *H264DirectStreamProcessor) Run() error {
	h.wg.Add(1)
	defer h.wg.Done()

	for {
		select {
		case <-h.done:
			return nil
		default:
		}

		rtpPacket, _, err := h.videoTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("error reading RTP packet: %w", err)
		}

		// H264パケットを処理（古い実装と同じロジック）
		if err := h.handleH264Packet(rtpPacket.Payload); err != nil {
			return err
		}
	}
}

// Stop は処理を停止
func (h *H264DirectStreamProcessor) Stop() error {
	close(h.done)
	h.wg.Wait()
	return nil
}

// handleH264Packet はH264パケットを処理して即座に書き込む
func (h *H264DirectStreamProcessor) handleH264Packet(payload []byte) error {
	if len(payload) < 1 {
		return nil
	}

	nalType := payload[0] & 0x1F

	// Check for start of new NAL unit
	if nalType >= 1 && nalType <= 23 {
		// Single NAL unit packet
		// Write start code
		startCode := []byte{0x00, 0x00, 0x00, 0x01}
		if _, err := h.writer.Write(startCode); err != nil {
			return fmt.Errorf("error writing start code: %w", err)
		}

		// Write NAL unit
		if _, err := h.writer.Write(payload); err != nil {
			return fmt.Errorf("error writing NAL unit: %w", err)
		}
	} else if nalType == 28 {
		// FU-A fragmentation
		if len(payload) < 2 {
			return nil
		}

		fuHeader := payload[1]
		isStart := (fuHeader & 0x80) != 0
		isEnd := (fuHeader & 0x40) != 0

		if isStart {
			// Start of fragmented NAL
			h.nalBuffer = nil
			// Reconstruct NAL header
			nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
			h.nalBuffer = append(h.nalBuffer, nalHeader)
		}

		// Append fragment data
		if len(payload) > 2 {
			h.nalBuffer = append(h.nalBuffer, payload[2:]...)
		}

		if isEnd && len(h.nalBuffer) > 0 {
			// End of fragmented NAL - write it out
			startCode := []byte{0x00, 0x00, 0x00, 0x01}
			if _, err := h.writer.Write(startCode); err != nil {
				return fmt.Errorf("error writing start code: %w", err)
			}

			if _, err := h.writer.Write(h.nalBuffer); err != nil {
				return fmt.Errorf("error writing NAL unit: %w", err)
			}
			h.nalBuffer = nil
		}
	}

	return nil
}
