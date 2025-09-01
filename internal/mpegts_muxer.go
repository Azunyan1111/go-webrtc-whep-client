package internal

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

const (
	videoPID = 256
	audioPID = 257
	pmtPID   = 4096
	pcrPID   = videoPID
)

type MPEGTSMuxer struct {
	videoTrack *webrtc.TrackRemote
	audioTrack *webrtc.TrackRemote
	writer     io.Writer

	// タイミング管理
	startTime   time.Time
	videoBaseTS uint32
	audioBaseTS uint32
	pcrBase     uint64

	// バッファ管理
	videoBuffer []byte
	audioBuffer []byte
	videoMutex  sync.Mutex
	audioMutex  sync.Mutex

	// PAT/PMT
	pat         []byte
	pmt         []byte
	packetCount uint64
}

func NewMPEGTSMuxer(w io.Writer, videoTrack, audioTrack *webrtc.TrackRemote) *MPEGTSMuxer {
	m := &MPEGTSMuxer{
		videoTrack: videoTrack,
		audioTrack: audioTrack,
		writer:     w,
		startTime:  time.Now(),
	}

	// PAT/PMT生成
	m.generatePAT()
	m.generatePMT()

	return m
}

func (m *MPEGTSMuxer) generatePAT() {
	// PAT section data
	patSection := &bytes.Buffer{}

	// Table ID for PAT
	patSection.WriteByte(0x00)

	// Section syntax indicator (1) + '0' + reserved (2) + section length
	sectionLength := uint16(13) // Fixed for single program
	patSection.WriteByte(0x80 | byte(sectionLength>>8))
	patSection.WriteByte(byte(sectionLength))

	// Transport stream ID
	binary.Write(patSection, binary.BigEndian, uint16(1))

	// Reserved (2) + version (5) + current/next
	patSection.WriteByte(0xC1)

	// Section number
	patSection.WriteByte(0x00)

	// Last section number
	patSection.WriteByte(0x00)

	// Program number
	binary.Write(patSection, binary.BigEndian, uint16(1))

	// Reserved (3) + PMT PID
	binary.Write(patSection, binary.BigEndian, uint16(0xE000|pmtPID))

	// CRC32 (simplified - in production use proper CRC)
	binary.Write(patSection, binary.BigEndian, uint32(0))

	m.pat = m.createTSPacket(0, patSection.Bytes(), true)
}

func (m *MPEGTSMuxer) generatePMT() {
	// PMT section data
	pmtSection := &bytes.Buffer{}

	// Table ID for PMT
	pmtSection.WriteByte(0x02)

	// Section syntax indicator (1) + '0' + reserved (2) + section length
	sectionLength := uint16(13) // Adjusted for video only or video+audio
	if m.audioTrack != nil {
		sectionLength = 23 // Both streams
	}
	pmtSection.WriteByte(0x80 | byte(sectionLength>>8))
	pmtSection.WriteByte(byte(sectionLength))

	// Program number
	binary.Write(pmtSection, binary.BigEndian, uint16(1))

	// Reserved (2) + version (5) + current/next
	pmtSection.WriteByte(0xC1)

	// Section number
	pmtSection.WriteByte(0x00)

	// Last section number
	pmtSection.WriteByte(0x00)

	// Reserved (3) + PCR PID
	binary.Write(pmtSection, binary.BigEndian, uint16(0xE000|pcrPID))

	// Reserved (4) + program info length
	binary.Write(pmtSection, binary.BigEndian, uint16(0xF000))

	// Video stream
	pmtSection.WriteByte(0x1B) // H.264 stream type
	// Reserved (3) + elementary PID
	binary.Write(pmtSection, binary.BigEndian, uint16(0xE000|videoPID))
	// Reserved (4) + ES info length
	binary.Write(pmtSection, binary.BigEndian, uint16(0xF000))

	// Audio stream (using MPEG-4 AAC LATM for Opus)
	pmtSection.WriteByte(0x11) // MPEG-4 AAC LATM
	// Reserved (3) + elementary PID
	binary.Write(pmtSection, binary.BigEndian, uint16(0xE000|audioPID))
	// Reserved (4) + ES info length
	binary.Write(pmtSection, binary.BigEndian, uint16(0xF000))

	// CRC32 (simplified - in production use proper CRC)
	binary.Write(pmtSection, binary.BigEndian, uint32(0))

	m.pmt = m.createTSPacket(pmtPID, pmtSection.Bytes(), true)
}

func (m *MPEGTSMuxer) createTSPacket(pid uint16, payload []byte, unitStart bool) []byte {
	packet := make([]byte, 188)

	// Sync byte
	packet[0] = 0x47

	// Error indicator (0) + Unit start + priority (0) + PID
	if unitStart {
		packet[1] = 0x40 | byte(pid>>8)
	} else {
		packet[1] = byte(pid >> 8)
	}
	packet[2] = byte(pid)

	// Scrambling (00) + Adaptation field (01 = payload only) + continuity counter
	packet[3] = 0x10 | byte(m.packetCount%16)

	m.packetCount++

	// Copy payload
	copy(packet[4:], payload)

	// Pad with 0xFF if necessary
	for i := 4 + len(payload); i < 188; i++ {
		packet[i] = 0xFF
	}

	return packet
}

func (m *MPEGTSMuxer) Run() {
	// 定期的にPAT/PMTを送信
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	fmt.Fprintf(os.Stderr, "MPEG-TS Muxer started - Video: %v, Audio: %v\n",
		m.videoTrack != nil, m.audioTrack != nil)

	// Stream processing is now handled by StreamManager

	// メインループ
	for range ticker.C {
		// PAT送信
		if _, err := m.writer.Write(m.pat); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing PAT: %v\n", err)
			return
		}

		// PMT送信
		if _, err := m.writer.Write(m.pmt); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing PMT: %v\n", err)
			return
		}
	}
}

// processVideoStream and processAudioStream are removed as they are now handled by StreamManager

func (m *MPEGTSMuxer) writeVideoPES(nalData []byte, rtpTimestamp uint32) {
	// PTS計算 (90kHz)
	pts := m.calculatePTS(rtpTimestamp, m.videoBaseTS, 90000)

	// PESパケット作成
	pesData := m.createPESPacket(0xE0, nalData, pts, pts)

	// TSパケット化して送信
	m.writePESAsTS(videoPID, pesData)
}


func (m *MPEGTSMuxer) writeAudioPES(opusData []byte, rtpTimestamp uint32) {
	// PTS計算 (48kHz for Opus)
	pts := m.calculatePTS(rtpTimestamp, m.audioBaseTS, 48000)

	// デバッグログ
	if DebugMode && len(opusData) > 0 {
		fmt.Fprintf(os.Stderr, "Audio PES: size=%d, pts=%d\n", len(opusData), pts)
	}

	// PESパケット作成
	pesData := m.createPESPacket(0xBD, opusData, pts, 0) // Private stream 1 for Opus

	// TSパケット化して送信
	m.writePESAsTS(audioPID, pesData)
}

func (m *MPEGTSMuxer) writePESAsTS(pid uint16, pesData []byte) {
	// PESデータを188バイトのTSパケットに分割
	offset := 0
	firstPacket := true

	for offset < len(pesData) {
		// ペイロードサイズ計算
		payloadSize := 184 // TSパケットサイズ - ヘッダー
		if offset+payloadSize > len(pesData) {
			payloadSize = len(pesData) - offset
		}

		// TSパケット作成
		tsPacket := m.createTSPacket(pid, pesData[offset:offset+payloadSize], firstPacket)

		// 送信
		if _, err := m.writer.Write(tsPacket); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing TS packet: %v\n", err)
			return
		}

		offset += payloadSize
		firstPacket = false
	}
}

func (m *MPEGTSMuxer) calculatePTS(rtpTimestamp, baseTimestamp uint32, clockRate uint32) uint64 {
	// RTPタイムスタンプの差分を計算
	timeDiff := rtpTimestamp - baseTimestamp

	// 90kHzのPTSに変換
	pts := uint64(timeDiff) * 90000 / uint64(clockRate)

	return pts
}

func (m *MPEGTSMuxer) createPESPacket(streamID byte, data []byte, pts, dts uint64) []byte {
	pesPacket := &bytes.Buffer{}

	// PES start code prefix
	pesPacket.WriteByte(0x00)
	pesPacket.WriteByte(0x00)
	pesPacket.WriteByte(0x01)

	// Stream ID
	pesPacket.WriteByte(streamID)

	// PES packet length (0 = unbounded)
	binary.Write(pesPacket, binary.BigEndian, uint16(0))

	// Optional PES header
	// '10' + scrambling (00) + priority + alignment + copyright + original
	pesPacket.WriteByte(0x80)

	// PTS DTS flags + 5 flags
	if dts != 0 && dts != pts {
		pesPacket.WriteByte(0xC0) // Both PTS and DTS
	} else {
		pesPacket.WriteByte(0x80) // PTS only
	}

	// PES header data length
	if dts != 0 && dts != pts {
		pesPacket.WriteByte(10) // PTS + DTS
	} else {
		pesPacket.WriteByte(5) // PTS only
	}

	// Write PTS
	m.writePTSDTS(pesPacket, pts, 0x20)

	// Write DTS if different from PTS
	if dts != 0 && dts != pts {
		m.writePTSDTS(pesPacket, dts, 0x10)
	}

	// Write payload data
	if streamID == 0xE0 {
		// Add H264 start code for video
		pesPacket.Write([]byte{0x00, 0x00, 0x00, 0x01})
	}
	pesPacket.Write(data)

	return pesPacket.Bytes()
}

func (m *MPEGTSMuxer) writePTSDTS(buf *bytes.Buffer, timestamp uint64, marker byte) {
	buf.WriteByte(marker | 0x01 | byte((timestamp>>29)&0x0E))
	buf.WriteByte(byte(timestamp >> 22))
	buf.WriteByte(0x01 | byte((timestamp>>14)&0xFE))
	buf.WriteByte(byte(timestamp >> 7))
	buf.WriteByte(0x01 | byte((timestamp<<1)&0xFE))
}
