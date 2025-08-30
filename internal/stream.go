package internal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// pipeRawStream pipes raw codec data to a writer (typically stdout for ffplay)
func PipeRawStream(track *webrtc.TrackRemote, w io.Writer, codecType string) {
	// Buffer for accumulating NAL units
	var nalBuffer []byte

	// For VP8/VP9 IVF output
	var ivfHeaderWritten bool
	var frameCount uint32
	var firstTimestamp uint32
	var currentFrame []byte
	var seenKeyFrame bool

	// For OggOpus output
	var oggHeaderWritten bool
	var granulePosition uint64
	var pageSequence uint32
	var serialNumber uint32 = 0x12345678 // arbitrary serial number

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "Error reading RTP packet: %v\n", err)
			return
		}

		// Extract payload
		payload := rtpPacket.Payload

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			switch strings.ToLower(codecType) {
			case "h264":
				handleH264Packet(payload, &nalBuffer, w)
			case "vp8":
				handleVP8Packet(payload, rtpPacket, &ivfHeaderWritten, &firstTimestamp, 
					&currentFrame, &seenKeyFrame, &frameCount, w)
			case "vp9":
				handleVP9Packet(payload, rtpPacket, &ivfHeaderWritten, &firstTimestamp,
					&currentFrame, &seenKeyFrame, &frameCount, w)
			default:
				// For other codecs, write raw payload
				if _, err := w.Write(payload); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing video payload: %v\n", err)
					return
				}
			}
		} else {
			// For audio (Opus), write OggOpus container
			handleOpusPacket(payload, &oggHeaderWritten, &granulePosition, &pageSequence, serialNumber, w)
		}
	}
}

func handleH264Packet(payload []byte, nalBuffer *[]byte, w io.Writer) {
	if len(payload) < 1 {
		return
	}

	nalType := payload[0] & 0x1F

	// Check for start of new NAL unit
	if nalType >= 1 && nalType <= 23 {
		// Single NAL unit packet
		// Write start code
		startCode := []byte{0x00, 0x00, 0x00, 0x01}
		if _, err := w.Write(startCode); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing start code: %v\n", err)
			return
		}

		// Write NAL unit
		if _, err := w.Write(payload); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
			return
		}
	} else if nalType == 28 {
		// FU-A fragmentation
		if len(payload) < 2 {
			return
		}

		fuHeader := payload[1]
		isStart := (fuHeader & 0x80) != 0
		isEnd := (fuHeader & 0x40) != 0

		if isStart {
			// Start of fragmented NAL
			*nalBuffer = nil
			// Reconstruct NAL header
			nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
			*nalBuffer = append(*nalBuffer, nalHeader)
		}

		// Append fragment data
		if len(payload) > 2 {
			*nalBuffer = append(*nalBuffer, payload[2:]...)
		}

		if isEnd && len(*nalBuffer) > 0 {
			// End of fragmented NAL - write it out
			startCode := []byte{0x00, 0x00, 0x00, 0x01}
			if _, err := w.Write(startCode); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing start code: %v\n", err)
				return
			}

			if _, err := w.Write(*nalBuffer); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
				return
			}
			*nalBuffer = nil
		}
	}
}

func handleVP8Packet(payload []byte, rtpPacket *rtp.Packet, ivfHeaderWritten *bool, 
	firstTimestamp *uint32, currentFrame *[]byte, seenKeyFrame *bool, frameCount *uint32, w io.Writer) {
	
	// Write IVF header on first packet
	if !*ivfHeaderWritten {
		if err := WriteIVFHeader(w, "VP80"); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return
		}
		*ivfHeaderWritten = true
		*firstTimestamp = rtpPacket.Timestamp
	}

	// Parse VP8 payload
	if len(payload) < 1 {
		return
	}

	// VP8 payload descriptor
	var vp8Header int
	headerSize := 1

	// X bit check
	if payload[0]&0x80 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return
		}
		vp8Header = 1
	}

	// Check S bit for start of partition
	isStart := (payload[0] & 0x10) != 0

	// Skip VP8 payload descriptor
	if len(payload) <= vp8Header {
		return
	}

	payloadData := payload[headerSize:]

	// For VP8, check if this is a keyframe
	if isStart && len(payloadData) >= 3 {
		// VP8 bitstream - check P bit in first byte
		isKeyFrame := (payloadData[0] & 0x01) == 0
		if !*seenKeyFrame && !isKeyFrame {
			return
		}
		*seenKeyFrame = true
	}

	// Accumulate frame data
	if isStart {
		*currentFrame = nil
	}
	*currentFrame = append(*currentFrame, payloadData...)

	// Write frame when marker bit is set
	if rtpPacket.Marker && len(*currentFrame) > 0 {
		// Write IVF frame header
		frameHeader := make([]byte, 12)
		binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(*currentFrame)))
		// Calculate timestamp
		timestamp := (rtpPacket.Timestamp - *firstTimestamp) / 90 // Convert from 90kHz to milliseconds
		binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

		if _, err := w.Write(frameHeader); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
			return
		}

		if _, err := w.Write(*currentFrame); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing VP8 frame: %v\n", err)
			return
		}

		*frameCount++
		*currentFrame = nil
	}
}

func handleVP9Packet(payload []byte, rtpPacket *rtp.Packet, ivfHeaderWritten *bool,
	firstTimestamp *uint32, currentFrame *[]byte, seenKeyFrame *bool, frameCount *uint32, w io.Writer) {
	
	// Write IVF header on first packet
	if !*ivfHeaderWritten {
		if err := WriteIVFHeader(w, "VP90"); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			return
		}
		*ivfHeaderWritten = true
		*firstTimestamp = rtpPacket.Timestamp
	}

	// Parse VP9 payload
	if len(payload) < 1 {
		return
	}

	// VP9 payload descriptor
	headerSize := 1

	// I bit check (extended picture ID)
	if payload[0]&0x80 != 0 {
		headerSize++
		if len(payload) < headerSize {
			return
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
		return
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
		*seenKeyFrame = true
	} else if !*seenKeyFrame {
		return
	}

	// Accumulate frame data
	if isStart {
		*currentFrame = nil
	}
	*currentFrame = append(*currentFrame, payloadData...)

	// Write frame when we have end bit or marker
	if (isEnd || rtpPacket.Marker) && len(*currentFrame) > 0 {
		// Write IVF frame header
		frameHeader := make([]byte, 12)
		binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(*currentFrame)))
		// Calculate timestamp
		timestamp := (rtpPacket.Timestamp - *firstTimestamp) / 90 // Convert from 90kHz to milliseconds
		binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

		if _, err := w.Write(frameHeader); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
			return
		}

		if _, err := w.Write(*currentFrame); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing VP9 frame: %v\n", err)
			return
		}

		*frameCount++
		*currentFrame = nil
	}
}

func handleOpusPacket(payload []byte, oggHeaderWritten *bool, granulePosition *uint64,
	pageSequence *uint32, serialNumber uint32, w io.Writer) {
	
	if !*oggHeaderWritten {
		// Write OggOpus headers
		seq, err := WriteOggOpusHeader(w, serialNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing OggOpus header: %v\n", err)
			return
		}
		*pageSequence = seq
		*oggHeaderWritten = true
	}

	// Calculate duration in samples (48kHz)
	// Opus packets in WebRTC are typically 20ms (960 samples @ 48kHz)
	samplesPerPacket := uint64(960) // 20ms @ 48kHz
	*granulePosition += samplesPerPacket

	// Write Opus packet as Ogg page
	if err := WriteOggPage(w, payload, *granulePosition, false, false, false, serialNumber, pageSequence); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing Ogg page: %v\n", err)
		return
	}
}