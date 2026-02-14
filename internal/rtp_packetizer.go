package internal

import (
	"github.com/pion/rtp"
)

const (
	VP8PayloadType  = 97
	OpusPayloadType = 111
	VP8ClockRate    = 90000
	OpusClockRate   = 48000
	MaxRTPPayload   = 1200
)

type VP8Packetizer struct {
	sequenceNumber uint16
	ssrc           uint32
	clockRate      uint32
}

func NewVP8Packetizer(ssrc uint32) *VP8Packetizer {
	return &VP8Packetizer{
		sequenceNumber: 0,
		ssrc:           ssrc,
		clockRate:      VP8ClockRate,
	}
}

func (p *VP8Packetizer) Packetize(frame []byte, timestampMs int64, isKeyframe bool) []*rtp.Packet {
	if len(frame) == 0 {
		return nil
	}

	// Convert timestamp from ms to RTP timestamp (90kHz clock)
	timestamp := uint32(timestampMs * int64(p.clockRate) / 1000)

	var packets []*rtp.Packet
	remaining := frame
	isFirst := true

	for len(remaining) > 0 {
		payloadSize := len(remaining)
		if payloadSize > MaxRTPPayload-1 { // -1 for VP8 payload descriptor
			payloadSize = MaxRTPPayload - 1
		}

		// VP8 Payload Descriptor (minimal, 1 byte)
		// https://datatracker.ietf.org/doc/html/rfc7741
		var descriptor byte = 0
		if isFirst {
			descriptor |= 0x10 // S (start of partition)
		}

		payload := make([]byte, 1+payloadSize)
		payload[0] = descriptor
		copy(payload[1:], remaining[:payloadSize])

		isLast := len(remaining) <= payloadSize

		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Padding:        false,
				Extension:      false,
				Marker:         isLast,
				PayloadType:    VP8PayloadType,
				SequenceNumber: p.sequenceNumber,
				Timestamp:      timestamp,
				SSRC:           p.ssrc,
			},
			Payload: payload,
		}

		packets = append(packets, packet)
		p.sequenceNumber++

		remaining = remaining[payloadSize:]
		isFirst = false
	}

	return packets
}

func (p *VP8Packetizer) PacketizeAndWrite(frame []byte, timestampMs int64, _ bool, writePacket func(*rtp.Packet) error) (int, error) {
	if len(frame) == 0 {
		return 0, nil
	}

	// Convert timestamp from ms to RTP timestamp (90kHz clock)
	timestamp := uint32(timestampMs * int64(p.clockRate) / 1000)

	remaining := frame
	isFirst := true
	sentCount := 0

	for len(remaining) > 0 {
		payloadSize := len(remaining)
		if payloadSize > MaxRTPPayload-1 { // -1 for VP8 payload descriptor
			payloadSize = MaxRTPPayload - 1
		}

		// VP8 Payload Descriptor (minimal, 1 byte)
		var descriptor byte = 0
		if isFirst {
			descriptor |= 0x10 // S (start of partition)
		}

		payload := make([]byte, 1+payloadSize)
		payload[0] = descriptor
		copy(payload[1:], remaining[:payloadSize])

		isLast := len(remaining) <= payloadSize
		packet := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Padding:        false,
				Extension:      false,
				Marker:         isLast,
				PayloadType:    VP8PayloadType,
				SequenceNumber: p.sequenceNumber,
				Timestamp:      timestamp,
				SSRC:           p.ssrc,
			},
			Payload: payload,
		}

		if err := writePacket(packet); err != nil {
			return sentCount, err
		}

		sentCount++
		p.sequenceNumber++
		remaining = remaining[payloadSize:]
		isFirst = false
	}

	return sentCount, nil
}

type OpusPacketizer struct {
	sequenceNumber uint16
	ssrc           uint32
	clockRate      uint32
}

func NewOpusPacketizer(ssrc uint32) *OpusPacketizer {
	return &OpusPacketizer{
		sequenceNumber: 0,
		ssrc:           ssrc,
		clockRate:      OpusClockRate,
	}
}

func (p *OpusPacketizer) Packetize(frame []byte, timestampMs int64) *rtp.Packet {
	if len(frame) == 0 {
		return nil
	}

	// Convert timestamp from ms to RTP timestamp (48kHz clock)
	timestamp := uint32(timestampMs * int64(p.clockRate) / 1000)

	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Padding:        false,
			Extension:      false,
			Marker:         true,
			PayloadType:    OpusPayloadType,
			SequenceNumber: p.sequenceNumber,
			Timestamp:      timestamp,
			SSRC:           p.ssrc,
		},
		Payload: frame,
	}

	p.sequenceNumber++

	return packet
}
