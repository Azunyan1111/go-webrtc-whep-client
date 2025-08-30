package internal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/pion/webrtc/v4"
)

// WebM/Matroska EBML IDs
const (
	EBMLHeader      = 0x1A45DFA3
	Segment         = 0x18538067
	Info            = 0x1549A966
	Tracks          = 0x1654AE6B
	Cluster         = 0x1F43B675
	Timecode        = 0xE7
	SimpleBlock     = 0xA3
	
	// Info elements
	TimecodeScale   = 0x2AD7B1
	MuxingApp       = 0x4D80
	WritingApp      = 0x5741
	Duration        = 0x4489
	
	// Track elements
	TrackEntry      = 0xAE
	TrackNumber     = 0xD7
	TrackUID        = 0x73C5
	TrackType       = 0x83
	CodecID         = 0x86
	CodecPrivate    = 0x63A2
	Video           = 0xE0
	Audio           = 0xE1
	PixelWidth      = 0xB0
	PixelHeight     = 0xBA
	SamplingFrequency = 0xB5
	Channels        = 0x9F
	
	// Track types
	TrackTypeVideo  = 0x01
	TrackTypeAudio  = 0x02
)

type WebMMuxer struct {
	videoTrack *webrtc.TrackRemote
	audioTrack *webrtc.TrackRemote
	writer     io.Writer
	
	startTime        time.Time
	clusterTime      uint64
	videoTrackNum    uint64
	audioTrackNum    uint64
	segmentStart     int64
	clusterStart     int64
	currentCluster   []byte
	isHeaderWritten  bool
	bufWriter        *bufio.Writer
	firstVideoTS     uint32
	firstAudioTS     uint32
	hasFirstVideoTS  bool
	hasFirstAudioTS  bool
	sps              []byte
	pps              []byte
	mutex            sync.Mutex
}

func NewWebMMuxer(w io.Writer, videoTrack, audioTrack *webrtc.TrackRemote) *WebMMuxer {
	bufWriter := bufio.NewWriter(w)
	return &WebMMuxer{
		videoTrack:    videoTrack,
		audioTrack:    audioTrack,
		writer:        bufWriter,
		bufWriter:     bufWriter,
		startTime:     time.Now(),
		videoTrackNum: 1,
		audioTrackNum: 2,
	}
}

func (m *WebMMuxer) Run() {
	// Write EBML header
	m.writeEBMLHeader()
	
	// Start segment
	m.writeSegmentHeader()
	
	// Write Info
	m.writeInfo()
	
	// Write Tracks
	m.writeTracks()
	
	// Flush headers immediately
	m.bufWriter.Flush()
	m.isHeaderWritten = true
	
	// Create done channels
	videoDone := make(chan bool)
	audioDone := make(chan bool)
	
	// Start processing streams
	if m.videoTrack != nil {
		go func() {
			m.processVideoStream()
			videoDone <- true
		}()
	} else {
		close(videoDone)
	}
	
	if m.audioTrack != nil {
		go func() {
			m.processAudioStream()
			audioDone <- true
		}()
	} else {
		close(audioDone)
	}
	
	// Wait for both streams to finish
	<-videoDone
	<-audioDone
}

func (m *WebMMuxer) writeEBMLHeader() {
	header := []byte{
		0x1A, 0x45, 0xDF, 0xA3, // EBML
		0x9F, // size (31 bytes)
		0x42, 0x86, 0x81, 0x01, // EBMLVersion = 1
		0x42, 0xF7, 0x81, 0x01, // EBMLReadVersion = 1
		0x42, 0xF2, 0x81, 0x04, // EBMLMaxIDLength = 4
		0x42, 0xF3, 0x81, 0x08, // EBMLMaxSizeLength = 8
		0x42, 0x82, 0x88, 0x6D, 0x61, 0x74, 0x72, 0x6F, 0x73, 0x6B, 0x61, // DocType = "matroska"
		0x42, 0x87, 0x81, 0x04, // DocTypeVersion = 4
		0x42, 0x85, 0x81, 0x02, // DocTypeReadVersion = 2
	}
	m.writer.Write(header)
}

func (m *WebMMuxer) writeSegmentHeader() {
	// Segment with unknown size (0x01FFFFFFFFFFFFFF)
	m.writer.Write([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
}

func (m *WebMMuxer) writeInfo() {
	info := &bytes.Buffer{}
	
	// TimecodeScale (1ms = 1000000ns)
	m.writeEBMLElement(info, TimecodeScale, m.encodeUInt(1000000))
	
	// MuxingApp
	m.writeEBMLElement(info, MuxingApp, []byte("go-webrtc-whep-client"))
	
	// WritingApp
	m.writeEBMLElement(info, WritingApp, []byte("go-webrtc-whep-client"))
	
	// Write Info element
	m.writeEBMLElement(m.writer, Info, info.Bytes())
}

func (m *WebMMuxer) writeTracks() {
	tracks := &bytes.Buffer{}
	
	// Video track
	if m.videoTrack != nil {
		videoEntry := &bytes.Buffer{}
		m.writeEBMLElement(videoEntry, TrackNumber, m.encodeUInt(m.videoTrackNum))
		m.writeEBMLElement(videoEntry, TrackUID, m.encodeUInt(m.videoTrackNum))
		m.writeEBMLElement(videoEntry, TrackType, []byte{TrackTypeVideo})
		
		codecID := "V_VP8"
		if m.videoTrack.Codec().MimeType == webrtc.MimeTypeH264 {
			codecID = "V_MPEG4/ISO/AVC"
		} else if m.videoTrack.Codec().MimeType == webrtc.MimeTypeVP9 {
			codecID = "V_VP9"
		}
		m.writeEBMLElement(videoEntry, CodecID, []byte(codecID))
		
		// Video element
		videoSettings := &bytes.Buffer{}
		m.writeEBMLElement(videoSettings, PixelWidth, m.encodeUInt(1920))  // Default
		m.writeEBMLElement(videoSettings, PixelHeight, m.encodeUInt(1080)) // Default
		m.writeEBMLElement(videoEntry, Video, videoSettings.Bytes())
		
		m.writeEBMLElement(tracks, TrackEntry, videoEntry.Bytes())
	}
	
	// Audio track
	if m.audioTrack != nil {
		audioEntry := &bytes.Buffer{}
		m.writeEBMLElement(audioEntry, TrackNumber, m.encodeUInt(m.audioTrackNum))
		m.writeEBMLElement(audioEntry, TrackUID, m.encodeUInt(m.audioTrackNum))
		m.writeEBMLElement(audioEntry, TrackType, []byte{TrackTypeAudio})
		m.writeEBMLElement(audioEntry, CodecID, []byte("A_OPUS"))
		
		// Audio element
		audioSettings := &bytes.Buffer{}
		m.writeEBMLElement(audioSettings, SamplingFrequency, m.encodeFloat(48000))
		m.writeEBMLElement(audioSettings, Channels, m.encodeUInt(2))
		m.writeEBMLElement(audioEntry, Audio, audioSettings.Bytes())
		
		m.writeEBMLElement(tracks, TrackEntry, audioEntry.Bytes())
	}
	
	// Write Tracks element
	m.writeEBMLElement(m.writer, Tracks, tracks.Bytes())
}

func (m *WebMMuxer) processVideoStream() {
	var nalBuffer []byte
	var frameBuffer []byte
	var currentTimestamp uint32
	
	for {
		rtpPacket, _, err := m.videoTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading video RTP: %v\n", err)
			return
		}
		
		// Simple depacketization
		payload := rtpPacket.Payload
		if len(payload) < 1 {
			continue
		}
		
		// Initialize timestamp on first packet
		if !m.hasFirstVideoTS {
			m.firstVideoTS = rtpPacket.Timestamp
			m.hasFirstVideoTS = true
		}
		
		// For H264, accumulate NAL units
		if m.videoTrack.Codec().MimeType == webrtc.MimeTypeH264 {
			// Check if new frame (timestamp changed)
			if currentTimestamp != 0 && currentTimestamp != rtpPacket.Timestamp && len(frameBuffer) > 0 {
				// Write complete frame
				m.writeVideoFrame(frameBuffer, currentTimestamp)
				frameBuffer = nil
			}
			currentTimestamp = rtpPacket.Timestamp
			
			nalType := payload[0] & 0x1F
			if nalType >= 1 && nalType <= 23 {
				// Single NAL unit
				// Store SPS/PPS for later use
				if nalType == 7 { // SPS
					m.mutex.Lock()
					m.sps = make([]byte, len(payload))
					copy(m.sps, payload)
					m.mutex.Unlock()
				} else if nalType == 8 { // PPS
					m.mutex.Lock()
					m.pps = make([]byte, len(payload))
					copy(m.pps, payload)
					m.mutex.Unlock()
				}
				
				// Add start code and append to frame
				frameBuffer = append(frameBuffer, 0x00, 0x00, 0x00, 0x01)
				frameBuffer = append(frameBuffer, payload...)
			} else if nalType == 28 {
				// FU-A
				if len(payload) < 2 {
					continue
				}
				fuHeader := payload[1]
				isStart := (fuHeader & 0x80) != 0
				isEnd := (fuHeader & 0x40) != 0
				
				if isStart {
					nalBuffer = nil
					// Add start code
					nalBuffer = append(nalBuffer, 0x00, 0x00, 0x00, 0x01)
					nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
					nalBuffer = append(nalBuffer, nalHeader)
				}
				
				if len(payload) > 2 {
					nalBuffer = append(nalBuffer, payload[2:]...)
				}
				
				if isEnd && len(nalBuffer) > 0 {
					// Check if this is SPS/PPS
					if len(nalBuffer) > 4 {
						nalType := nalBuffer[4] & 0x1F
						if nalType == 7 { // SPS
							m.mutex.Lock()
							m.sps = make([]byte, len(nalBuffer)-4)
							copy(m.sps, nalBuffer[4:])
							m.mutex.Unlock()
						} else if nalType == 8 { // PPS
							m.mutex.Lock()
							m.pps = make([]byte, len(nalBuffer)-4)
							copy(m.pps, nalBuffer[4:])
							m.mutex.Unlock()
						}
					}
					
					// Append completed NAL to frame buffer
					frameBuffer = append(frameBuffer, nalBuffer...)
					nalBuffer = nil
				}
			} else if nalType == 24 {
				// STAP-A - aggregate packet
				if len(payload) > 1 {
					offset := 1
					for offset < len(payload)-2 {
						nalSize := int(payload[offset])<<8 | int(payload[offset+1])
						offset += 2
						if offset+nalSize <= len(payload) {
							// Check NAL type
							if nalSize > 0 {
								innerNalType := payload[offset] & 0x1F
								if innerNalType == 7 { // SPS
									m.mutex.Lock()
									m.sps = make([]byte, nalSize)
									copy(m.sps, payload[offset:offset+nalSize])
									m.mutex.Unlock()
								} else if innerNalType == 8 { // PPS
									m.mutex.Lock()
									m.pps = make([]byte, nalSize)
									copy(m.pps, payload[offset:offset+nalSize])
									m.mutex.Unlock()
								}
							}
							
							// Add start code and NAL unit
							frameBuffer = append(frameBuffer, 0x00, 0x00, 0x00, 0x01)
							frameBuffer = append(frameBuffer, payload[offset:offset+nalSize]...)
							offset += nalSize
						} else {
							break
						}
					}
				}
			}
		} else {
			// VP8/VP9 - write directly
			m.writeVideoFrame(payload, rtpPacket.Timestamp)
		}
	}
}

func (m *WebMMuxer) processAudioStream() {
	for {
		rtpPacket, _, err := m.audioTrack.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading audio RTP: %v\n", err)
			return
		}
		
		// Initialize timestamp on first packet
		if !m.hasFirstAudioTS {
			m.firstAudioTS = rtpPacket.Timestamp
			m.hasFirstAudioTS = true
		}
		
		// Write Opus frame
		m.writeAudioFrame(rtpPacket.Payload, rtpPacket.Timestamp)
	}
}

func (m *WebMMuxer) writeVideoFrame(data []byte, timestamp uint32) {
	// Calculate timecode in milliseconds (relative to first timestamp)
	relativeTS := timestamp - m.firstVideoTS
	timecode := uint64(relativeTS) / 90 // 90kHz to ms
	
	// Detect keyframe for H.264
	keyframe := false
	frameData := data
	
	if m.videoTrack.Codec().MimeType == webrtc.MimeTypeH264 && len(data) > 4 {
		// Check if frame contains IDR
		hasIDR := false
		hasSPS := false
		hasPPS := false
		
		// Scan all NAL units in frame
		i := 0
		for i < len(data)-4 {
			// Find start code
			if data[i] == 0 && data[i+1] == 0 && data[i+2] == 0 && data[i+3] == 1 {
				if i+4 < len(data) {
					nalType := data[i+4] & 0x1F
					if nalType == 5 {
						hasIDR = true
					} else if nalType == 7 {
						hasSPS = true
					} else if nalType == 8 {
						hasPPS = true
					}
				}
				i += 4
			} else {
				i++
			}
		}
		
		keyframe = hasIDR
		
		// If IDR frame but missing SPS/PPS, prepend them
		if hasIDR && (!hasSPS || !hasPPS) {
			m.mutex.Lock()
			if m.sps != nil && m.pps != nil {
				// Prepend SPS and PPS
				newData := make([]byte, 0, len(data)+len(m.sps)+len(m.pps)+8)
				newData = append(newData, 0x00, 0x00, 0x00, 0x01)
				newData = append(newData, m.sps...)
				newData = append(newData, 0x00, 0x00, 0x00, 0x01)
				newData = append(newData, m.pps...)
				newData = append(newData, data...)
				frameData = newData
			}
			m.mutex.Unlock()
		}
	} else {
		// For VP8/VP9, check for keyframe flag
		keyframe = true
	}
	
	m.writeSimpleBlock(m.videoTrackNum, frameData, timecode, keyframe)
}

func (m *WebMMuxer) writeAudioFrame(data []byte, timestamp uint32) {
	// Calculate timecode in milliseconds (relative to first timestamp)
	relativeTS := timestamp - m.firstAudioTS
	timecode := uint64(relativeTS) / 48 // 48kHz to ms
	
	m.writeSimpleBlock(m.audioTrackNum, data, timecode, false)
}

func (m *WebMMuxer) writeSimpleBlock(trackNum uint64, data []byte, timecode uint64, keyframe bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	
	// Start new cluster every second
	if timecode - m.clusterTime > 1000 || m.clusterTime == 0 {
		m.startNewCluster(timecode)
	}
	
	block := &bytes.Buffer{}
	
	// Track number (variable size integer)
	m.writeVarInt(block, trackNum)
	
	// Timecode (relative to cluster)
	relativeTime := int16(timecode - m.clusterTime)
	binary.Write(block, binary.BigEndian, relativeTime)
	
	// Flags
	flags := byte(0)
	if keyframe {
		flags |= 0x80
	}
	block.WriteByte(flags)
	
	// Frame data
	block.Write(data)
	
	// Write SimpleBlock
	m.writeEBMLElement(m.writer, SimpleBlock, block.Bytes())
	
	// Flush immediately for streaming
	if m.isHeaderWritten {
		m.bufWriter.Flush()
	}
}

func (m *WebMMuxer) startNewCluster(timecode uint64) {
	m.clusterTime = timecode
	
	// Write Cluster element with unknown size
	m.writer.Write([]byte{0x1F, 0x43, 0xB6, 0x75, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	
	// Write Timecode
	m.writeEBMLElement(m.writer, Timecode, m.encodeUInt(timecode))
}

func (m *WebMMuxer) writeEBMLElement(w io.Writer, id uint32, data []byte) {
	// Write ID
	m.writeEBMLID(w, id)
	
	// Write size
	m.writeVarInt(w, uint64(len(data)))
	
	// Write data
	w.Write(data)
}

func (m *WebMMuxer) writeEBMLID(w io.Writer, id uint32) {
	if id <= 0xFF {
		w.Write([]byte{byte(id)})
	} else if id <= 0xFFFF {
		binary.Write(w, binary.BigEndian, uint16(id))
	} else if id <= 0xFFFFFF {
		w.Write([]byte{byte(id >> 16), byte(id >> 8), byte(id)})
	} else {
		binary.Write(w, binary.BigEndian, id)
	}
}

func (m *WebMMuxer) writeVarInt(w io.Writer, n uint64) {
	if n < 127 {
		w.Write([]byte{byte(n | 0x80)})
	} else if n < 16383 {
		w.Write([]byte{byte((n>>8) | 0x40), byte(n)})
	} else if n < 2097151 {
		w.Write([]byte{byte((n>>16) | 0x20), byte(n >> 8), byte(n)})
	} else if n < 268435455 {
		w.Write([]byte{byte((n>>24) | 0x10), byte(n >> 16), byte(n >> 8), byte(n)})
	}
}

func (m *WebMMuxer) encodeUInt(n uint64) []byte {
	buf := make([]byte, 8)
	size := 0
	for i := 7; i >= 0; i-- {
		if n > 0 || size > 0 {
			buf[size] = byte(n >> (uint(i) * 8))
			size++
		}
	}
	if size == 0 {
		return []byte{0}
	}
	return buf[:size]
}

func (m *WebMMuxer) encodeFloat(f float64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, *(*uint64)(unsafe.Pointer(&f)))
	return buf
}

func (m *WebMMuxer) encodeFloat32(f float32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, *(*uint32)(unsafe.Pointer(&f)))
	return buf
}