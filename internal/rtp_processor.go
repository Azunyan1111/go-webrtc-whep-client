package internal

import (
	"github.com/pion/rtp"
)

// DefaultRTPProcessor は標準的なRTPパケット処理を実装
type DefaultRTPProcessor struct {
	currentFrame   []byte
	firstTimestamp uint32
	seenKeyFrame   bool
	lastTimestamp  uint32
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

// processVP8Packet はVP8 RTPパケットを処理
// RFC 7741に基づくVP8ペイロードデスクリプタの解析
func (p *DefaultRTPProcessor) processVP8Packet(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	if len(payload) < 1 {
		return nil, nil
	}

	// タイムスタンプが変わった場合、前のフレームをリセット
	if p.lastTimestamp != 0 && p.lastTimestamp != packet.Timestamp {
		// 新しいフレームが始まった
		p.currentFrame = nil
	}
	p.lastTimestamp = packet.Timestamp

	// VP8 payload descriptor parsing (RFC 7741)
	// First byte:
	//  X R N S R PID
	//  X: Extension bit
	//  R: Reserved
	//  N: Non-reference frame
	//  S: Start of VP8 partition
	//  PID: Partition index (3 bits)

	headerSize := 1
	firstByte := payload[0]

	// Check S bit for start of partition
	isStart := (firstByte & 0x10) != 0

	// X bit - extension present
	if firstByte&0x80 != 0 {
		if len(payload) < 2 {
			return nil, nil
		}
		headerSize++
		extByte := payload[1]

		// I bit - PictureID present
		if extByte&0x80 != 0 {
			headerSize++
			if len(payload) < headerSize {
				return nil, nil
			}
			// M bit - PictureID is 16 bits
			if payload[headerSize-1]&0x80 != 0 {
				headerSize++
			}
		}

		// L bit - TL0PICIDX present
		if extByte&0x40 != 0 {
			headerSize++
		}

		// T or K bit - TID/KEYIDX present
		if extByte&0x20 != 0 || extByte&0x10 != 0 {
			headerSize++
		}
	}

	if len(payload) <= headerSize {
		return nil, nil
	}

	payloadData := payload[headerSize:]

	// キーフレームチェック - VP8ビットストリームの最初のバイトで判定
	// VP8 keyframe: bit 0 = 0 (P bit), and starts with sync code after frame tag
	isKeyFrame := false
	if isStart && len(payloadData) >= 10 {
		// VP8 uncompressed data chunk starts with:
		// - frame_tag (3 bytes): bit 0 is key_frame (0=key, 1=inter)
		// - For keyframes: sync code (3 bytes): 0x9d 0x01 0x2a
		frameTag := payloadData[0]
		isKeyFrame = (frameTag & 0x01) == 0

		if isKeyFrame {
			// キーフレームの場合、sync codeを確認
			if payloadData[3] == 0x9d && payloadData[4] == 0x01 && payloadData[5] == 0x2a {
				DebugLog("VP8 keyframe detected: sync code OK\n")
				p.seenKeyFrame = true
			} else {
				DebugLog("VP8 keyframe but no sync code: %x\n", payloadData[:10])
				// sync codeがなくても、frame_tagがキーフレームを示していれば受け入れる
				p.seenKeyFrame = true
			}
		}
	}

	// キーフレームをまだ見ていない場合はスキップ
	if !p.seenKeyFrame {
		return nil, nil
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
// draft-ietf-payload-vp9に基づくVP9ペイロードデスクリプタの解析
func (p *DefaultRTPProcessor) processVP9Packet(packet *rtp.Packet) ([][]byte, error) {
	payload := packet.Payload
	if len(payload) < 1 {
		return nil, nil
	}

	// タイムスタンプが変わった場合、前のフレームをリセット
	if p.lastTimestamp != 0 && p.lastTimestamp != packet.Timestamp {
		p.currentFrame = nil
	}
	p.lastTimestamp = packet.Timestamp

	// VP9 payload descriptor parsing
	// First byte:
	//  I P L F B E V Z
	//  I: Picture ID present
	//  P: Inter-picture predicted layer frame
	//  L: Layer indices present
	//  F: Flexible mode
	//  B: Start of frame
	//  E: End of frame
	//  V: Scalability structure present
	//  Z: Not a reference frame for upper spatial layers

	headerSize := 1
	firstByte := payload[0]

	// B bit - beginning of frame
	isStart := (firstByte & 0x08) != 0
	// E bit - end of frame
	isEnd := (firstByte & 0x04) != 0
	// P bit - inter-picture predicted frame (0 = keyframe)
	isInterFrame := (firstByte & 0x40) != 0

	// I bit - Picture ID present
	if firstByte&0x80 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return nil, nil
		}
		// M bit - Picture ID is 16 bits
		if payload[headerSize-1]&0x80 != 0 {
			headerSize++
		}
	}

	// L bit - Layer indices present
	if firstByte&0x20 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return nil, nil
		}
		// Check if TL0PICIDX is present (non-flexible mode)
		if (firstByte & 0x10) == 0 {
			headerSize++
		}
	}

	// F bit - Flexible mode, reference indices present
	if (firstByte&0x10) != 0 && isInterFrame {
		// In flexible mode with P=1, we have reference indices
		// Each P_DIFF is 1 byte, N bit indicates more follow
		if len(payload) > headerSize {
			for {
				if len(payload) <= headerSize {
					break
				}
				refByte := payload[headerSize]
				headerSize++
				// N bit - more reference indices follow
				if refByte&0x01 == 0 {
					break
				}
			}
		}
	}

	// V bit - Scalability structure present
	if firstByte&0x02 != 0 {
		if len(payload) <= headerSize {
			return nil, nil
		}
		ssHeader := payload[headerSize]
		headerSize++

		nSpatial := ((ssHeader >> 5) & 0x07) + 1
		yBit := (ssHeader & 0x10) != 0
		gBit := (ssHeader & 0x08) != 0

		if yBit {
			headerSize += int(nSpatial) * 4 // WIDTH (2) + HEIGHT (2) per spatial layer
		}

		if gBit {
			if len(payload) <= headerSize {
				return nil, nil
			}
			nG := payload[headerSize]
			headerSize++
			headerSize += int(nG) * 3 // Each PG has 3 bytes
		}
	}

	if len(payload) <= headerSize {
		return nil, nil
	}

	payloadData := payload[headerSize:]

	// Check if keyframe (P=0 means keyframe)
	if isStart && !isInterFrame {
		DebugLog("VP9 keyframe detected\n")
		p.seenKeyFrame = true
	}

	// キーフレームをまだ見ていない場合はスキップ
	if !p.seenKeyFrame {
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
