package internal

import (
	"fmt"

	"github.com/pion/rtp"
)

// DefaultRTPProcessor は標準的なRTPパケット処理を実装
type DefaultRTPProcessor struct {
	nalBuffer         []byte
	currentFrame      []byte
	firstTimestamp    uint32
	seenKeyFrame      bool
	h264FrameBuffer   []byte // H264フレーム全体を蓄積
	lastH264Timestamp uint32
}

// NewDefaultRTPProcessor は新しいRTPプロセッサを作成
func NewDefaultRTPProcessor() RTPProcessor {
	return &DefaultRTPProcessor{}
}

// ProcessRTPPacket はRTPパケットを処理してメディアデータを抽出
func (p *DefaultRTPProcessor) ProcessRTPPacket(packet *rtp.Packet, codecType string) ([][]byte, error) {
	if packet == nil || len(packet.Payload) == 0 {
		return nil, nil
	}

	switch codecType {
	case "h264":
		return p.processH264Packet(packet)
	case "vp8":
		return p.processVP8Packet(packet)
	case "vp9":
		return p.processVP9Packet(packet)
	case "opus":
		// Opusはシンプルにペイロードを返す
		return [][]byte{packet.Payload}, nil
	default:
		return [][]byte{packet.Payload}, nil
	}
}

// processH264Packet はH264 RTPパケットを処理
func (p *DefaultRTPProcessor) processH264Packet(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	if len(payload) < 1 {
		return nil, nil
	}

	// タイムスタンプが変わったら前のフレームを返す
	if p.lastH264Timestamp != 0 && p.lastH264Timestamp != packet.Timestamp && len(p.h264FrameBuffer) > 0 {
		frame := make([]byte, len(p.h264FrameBuffer))
		copy(frame, p.h264FrameBuffer)
		p.h264FrameBuffer = nil
		p.lastH264Timestamp = packet.Timestamp
		return [][]byte{frame}, nil
	}
	p.lastH264Timestamp = packet.Timestamp

	nalType := payload[0] & 0x1F

	switch {
	case nalType >= 1 && nalType <= 23:
		// Single NAL unit - スタートコード付きでバッファに追加
		p.h264FrameBuffer = append(p.h264FrameBuffer, 0x00, 0x00, 0x00, 0x01)
		p.h264FrameBuffer = append(p.h264FrameBuffer, payload...)

	case nalType == 24:
		// STAP-A - aggregate packet
		if len(payload) > 1 {
			offset := 1
			for offset < len(payload)-2 {
				nalSize := int(payload[offset])<<8 | int(payload[offset+1])
				offset += 2
				if offset+nalSize <= len(payload) {
					// 各NALユニットにスタートコードを付けてバッファに追加
					p.h264FrameBuffer = append(p.h264FrameBuffer, 0x00, 0x00, 0x00, 0x01)
					p.h264FrameBuffer = append(p.h264FrameBuffer, payload[offset:offset+nalSize]...)
					offset += nalSize
				} else {
					break
				}
			}
		}

	case nalType == 28:
		// FU-A fragmentation
		if len(payload) < 2 {
			return nil, nil
		}

		fuHeader := payload[1]
		isStart := (fuHeader & 0x80) != 0
		isEnd := (fuHeader & 0x40) != 0

		if isStart {
			// Start of fragmented NAL
			p.nalBuffer = nil
			// スタートコードを追加
			p.nalBuffer = append(p.nalBuffer, 0x00, 0x00, 0x00, 0x01)
			// Reconstruct NAL header
			nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
			p.nalBuffer = append(p.nalBuffer, nalHeader)
		}

		// Append fragment data
		if len(payload) > 2 {
			p.nalBuffer = append(p.nalBuffer, payload[2:]...)
		}

		if isEnd && len(p.nalBuffer) > 0 {
			// End of fragmented NAL - バッファに追加
			p.h264FrameBuffer = append(p.h264FrameBuffer, p.nalBuffer...)
			p.nalBuffer = nil
		}

	default:
		return nil, fmt.Errorf("unsupported H264 NAL type: %d", nalType)
	}

	// マーカービットがセットされている場合はフレーム終了
	if packet.Marker && len(p.h264FrameBuffer) > 0 {
		frame := make([]byte, len(p.h264FrameBuffer))
		copy(frame, p.h264FrameBuffer)
		p.h264FrameBuffer = nil
		return [][]byte{frame}, nil
	}

	return nil, nil
}

// processVP8Packet はVP8 RTPパケットを処理
func (p *DefaultRTPProcessor) processVP8Packet(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	if len(payload) < 1 {
		return nil, nil
	}

	// Initialize timestamp on first packet
	if p.firstTimestamp == 0 {
		p.firstTimestamp = packet.Timestamp
	}

	// VP8 payload descriptor
	headerSize := 1

	// X bit check
	if payload[0]&0x80 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return nil, nil
		}
	}

	// Check S bit for start of partition
	isStart := (payload[0] & 0x10) != 0

	// Skip VP8 payload descriptor
	if len(payload) <= headerSize {
		return nil, nil
	}

	payloadData := payload[headerSize:]

	// For VP8, check if this is a keyframe
	if isStart && len(payloadData) >= 3 {
		// VP8 bitstream - check P bit in first byte
		isKeyFrame := (payloadData[0] & 0x01) == 0
		if !p.seenKeyFrame && !isKeyFrame {
			return nil, nil
		}
		p.seenKeyFrame = true
	}

	// Accumulate frame data
	if isStart {
		p.currentFrame = nil
	}
	p.currentFrame = append(p.currentFrame, payloadData...)

	// Return frame when marker bit is set
	if packet.Marker && len(p.currentFrame) > 0 {
		frame := make([]byte, len(p.currentFrame))
		copy(frame, p.currentFrame)
		p.currentFrame = nil
		return [][]byte{frame}, nil
	}

	return nil, nil
}

// processVP9Packet はVP9 RTPパケットを処理
func (p *DefaultRTPProcessor) processVP9Packet(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	if len(payload) < 1 {
		return nil, nil
	}

	// Initialize timestamp on first packet
	if p.firstTimestamp == 0 {
		p.firstTimestamp = packet.Timestamp
	}

	// VP9 payload descriptor
	headerSize := 1

	// I bit check (extended picture ID)
	if payload[0]&0x80 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return nil, nil
		}
		// M bit check (extended picture ID is 2 bytes)
		if payload[1]&0x80 != 0 {
			headerSize++
		}
	}

	// L bit check (TID/SLID present)
	if payload[0]&0x40 != 0 {
		headerSize++
	}

	// Check if we have flexible mode
	if payload[0]&0x10 != 0 {
		// F=1, flexible mode
		headerSize++ // For TID/U/SID/D
	}

	if len(payload) < headerSize {
		return nil, nil
	}

	// B bit - beginning of frame
	isStart := (payload[0] & 0x08) != 0
	// E bit - end of frame
	isEnd := (payload[0] & 0x04) != 0
	// P bit - inter-picture predicted frame
	isInterFrame := (payload[0] & 0x01) != 0

	// Skip VP9 payload descriptor
	payloadData := payload[headerSize:]

	// Check if keyframe
	if isStart && !isInterFrame {
		p.seenKeyFrame = true
	} else if !p.seenKeyFrame {
		return nil, nil
	}

	// Accumulate frame data
	if isStart {
		p.currentFrame = nil
	}
	p.currentFrame = append(p.currentFrame, payloadData...)

	// Return frame when we have end bit or marker
	if (isEnd || packet.Marker) && len(p.currentFrame) > 0 {
		frame := make([]byte, len(p.currentFrame))
		copy(frame, p.currentFrame)
		p.currentFrame = nil
		return [][]byte{frame}, nil
	}

	return nil, nil
}
