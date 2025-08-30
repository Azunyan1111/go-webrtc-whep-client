package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/spf13/pflag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// debugLog prints debug messages only when debug mode is enabled
func debugLog(format string, v ...interface{}) {
	if debugMode {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format, v...)
	}
}

var (
	whepURL    string
	videoPipe  bool
	audioPipe  bool
	videoCodec string
	listCodecs bool
	debugMode  bool
)

func init() {
	pflag.StringVarP(&whepURL, "url", "u", "http://localhost:8080/whep", "WHEP server URL")
	pflag.BoolVarP(&videoPipe, "video-pipe", "v", false, "Output raw video stream to stdout (for piping to ffplay)")
	pflag.BoolVarP(&audioPipe, "audio-pipe", "a", false, "Output raw Opus stream to stdout (for piping to ffplay)")
	pflag.StringVarP(&videoCodec, "codec", "c", "h264", "Video codec to use (h264, vp8, vp9)")
	pflag.BoolVarP(&listCodecs, "list-codecs", "l", false, "List codecs supported by the WHEP server")
	pflag.BoolVarP(&debugMode, "debug", "d", false, "Enable debug logging")
}

func main() {
	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "WHEP Native Client - Receive WebRTC streams via WHEP protocol\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --video-pipe | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --audio-pipe | ffplay -i -\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -u http://example.com/whep --list-codecs\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		pflag.PrintDefaults()
	}

	pflag.Parse()

	if listCodecs {
		if err := listServerCodecs(); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Validate pipe options
	if videoPipe && audioPipe {
		return fmt.Errorf("cannot pipe both video and audio to stdout simultaneously")
	}

	if videoPipe && audioPipe {
		return fmt.Errorf("cannot output both video and audio to stdout")
	}

	fmt.Fprintf(os.Stderr, "Connecting to WHEP server: %s\n", whepURL)
	fmt.Fprintf(os.Stderr, "Using video codec: %s\n", videoCodec)

	// Create a MediaEngine object to configure the supported codec
	mediaEngine := &webrtc.MediaEngine{}

	// Register video codec based on user selection
	switch strings.ToLower(videoCodec) {
	case "h264":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			},
			PayloadType: 96,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	case "vp8":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP8, ClockRate: 90000,
			},
			PayloadType: 97,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	case "vp9":
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: webrtc.MimeTypeVP9, ClockRate: 90000,
			},
			PayloadType: 98,
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported video codec: %s (supported: h264, vp8, vp9)", videoCodec)
	}

	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create an InterceptorRegistry
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	// Create the API object
	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create a new PeerConnection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer func() {
		if cErr := peerConnection.Close(); cErr != nil {
			fmt.Printf("cannot close peerConnection: %v\n", cErr)
		}
	}()

	// Create tracks for receiving
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		return err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
		return err
	}

	// Set handlers for incoming tracks
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		debugLog("Track received - Type: %s, Codec: %s\n", track.Kind(), codec.MimeType)

		if track.Kind() == webrtc.RTPCodecTypeVideo {
			if videoPipe {
				// Pipe raw video to stdout
				go pipeRawStream(track, os.Stdout, videoCodec)
			}
		} else {
			if audioPipe {
				// Pipe raw Opus to stdout
				go pipeRawStream(track, os.Stdout, "")
			}
		}
	})

	// Set ICE connection state handler
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		debugLog("ICE Connection State has changed: %s\n", connectionState.String())
		if connectionState == webrtc.ICEConnectionStateFailed {
			fmt.Fprintln(os.Stderr, "ICE Connection Failed")
			os.Exit(1)
		}
	})

	// Create offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	// Create gathering complete promise
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Set local description
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		return err
	}

	// Wait for ICE gathering to complete
	<-gatherComplete

	// Send offer to WHEP server
	fmt.Fprintln(os.Stderr, "Sending offer to WHEP server...")
	if debugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Offer ===\n%s\n=== End Offer ===\n\n", peerConnection.LocalDescription().SDP)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", whepURL, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
	if err != nil {
		return err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/sdp")

	// Send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("WHEP server returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read answer
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Set remote description
	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	})
	if err != nil {
		return err
	}

	if debugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Answer ===\n%s\n=== End Answer ===\n\n", string(answer))
	}
	fmt.Fprintln(os.Stderr, "Connected to WHEP server, receiving media...")

	if videoPipe {
		fmt.Fprintf(os.Stderr, "Piping raw %s video to stdout\n", strings.ToUpper(videoCodec))
	}

	if audioPipe {
		fmt.Fprintln(os.Stderr, "Piping raw Opus audio to stdout")
	}

	fmt.Fprintln(os.Stderr, "Press Ctrl+C to stop")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Fprintln(os.Stderr, "Closing...")
	return nil
}

// writeIVFHeader writes the IVF container header for VP8/VP9 codecs
func writeIVFHeader(w io.Writer, fourCC string) error {
	header := make([]byte, 32)
	copy(header[0:], "DKIF")                        // Signature
	binary.LittleEndian.PutUint16(header[4:], 0)    // Version
	binary.LittleEndian.PutUint16(header[6:], 32)   // Header size
	copy(header[8:], fourCC)                        // FourCC (VP80 or VP90)
	binary.LittleEndian.PutUint16(header[12:], 640) // Width
	binary.LittleEndian.PutUint16(header[14:], 480) // Height
	binary.LittleEndian.PutUint32(header[16:], 30)  // Framerate denominator
	binary.LittleEndian.PutUint32(header[20:], 1)   // Framerate numerator
	binary.LittleEndian.PutUint32(header[24:], 0)   // Frame count (placeholder)
	binary.LittleEndian.PutUint32(header[28:], 0)   // Unused

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("error writing IVF header: %v", err)
	}
	return nil
}

// writeOggOpusHeader writes the OggOpus header pages
func writeOggOpusHeader(w io.Writer, serialNumber uint32) (uint32, error) {
	pageSequence := uint32(0)

	// Create OpusHead header
	var opusHead bytes.Buffer
	opusHead.WriteString("OpusHead")
	opusHead.WriteByte(1)                                       // Version
	opusHead.WriteByte(2)                                       // Channel count (stereo)
	binary.Write(&opusHead, binary.LittleEndian, uint16(3840))  // Pre-skip (80ms @ 48kHz)
	binary.Write(&opusHead, binary.LittleEndian, uint32(48000)) // Input sample rate
	binary.Write(&opusHead, binary.LittleEndian, int16(0))      // Output gain
	opusHead.WriteByte(0)                                       // Channel mapping family

	// Write OpusHead page
	if err := writeOggPage(w, opusHead.Bytes(), 0, true, false, false, serialNumber, &pageSequence); err != nil {
		return pageSequence, err
	}

	// Create OpusTags header
	var opusTags bytes.Buffer
	opusTags.WriteString("OpusTags")
	vendor := "go-webrtc-whep-client"
	binary.Write(&opusTags, binary.LittleEndian, uint32(len(vendor)))
	opusTags.WriteString(vendor)
	binary.Write(&opusTags, binary.LittleEndian, uint32(0)) // No user comments

	// Write OpusTags page
	if err := writeOggPage(w, opusTags.Bytes(), 0, false, false, false, serialNumber, &pageSequence); err != nil {
		return pageSequence, err
	}

	return pageSequence, nil
}

// writeOggPage writes an Ogg page
func writeOggPage(w io.Writer, data []byte, granulePos uint64, bos, eos, continued bool, serialNumber uint32, pageSequence *uint32) error {
	// Create segments
	var segments [][]byte
	remaining := data
	for len(remaining) > 0 {
		segSize := 255
		if len(remaining) < 255 {
			segSize = len(remaining)
		}
		segments = append(segments, remaining[:segSize])
		remaining = remaining[segSize:]
	}

	// If data was exactly a multiple of 255, add empty segment
	if len(segments) > 0 && len(segments[len(segments)-1]) == 255 {
		segments = append(segments, []byte{})
	}

	// Build page header
	var header bytes.Buffer
	header.WriteString("OggS")
	header.WriteByte(0) // Version

	// Header type
	headerType := byte(0)
	if continued {
		headerType |= 0x01
	}
	if bos {
		headerType |= 0x02
	}
	if eos {
		headerType |= 0x04
	}
	header.WriteByte(headerType)

	binary.Write(&header, binary.LittleEndian, granulePos)
	binary.Write(&header, binary.LittleEndian, serialNumber)
	binary.Write(&header, binary.LittleEndian, *pageSequence)
	*pageSequence++

	// Checksum placeholder
	checksumOffset := header.Len()
	binary.Write(&header, binary.LittleEndian, uint32(0))

	// Segment table
	header.WriteByte(byte(len(segments)))
	for _, seg := range segments {
		header.WriteByte(byte(len(seg)))
	}

	// Combine header and data
	var page bytes.Buffer
	page.Write(header.Bytes())
	for _, seg := range segments {
		page.Write(seg)
	}

	// Calculate and set checksum
	pageBytes := page.Bytes()
	checksum := oggChecksum(pageBytes)
	binary.LittleEndian.PutUint32(pageBytes[checksumOffset:], checksum)

	_, err := w.Write(pageBytes)
	return err
}

// oggChecksum calculates the Ogg page CRC32 checksum
func oggChecksum(data []byte) uint32 {
	// Ogg CRC32 lookup table (polynomial 0x04c11db7)
	crcTable := []uint32{
		0x00000000, 0x04c11db7, 0x09823b6e, 0x0d4326d9,
		0x130476dc, 0x17c56b6b, 0x1a864db2, 0x1e475005,
		0x2608edb8, 0x22c9f00f, 0x2f8ad6d6, 0x2b4bcb61,
		0x350c9b64, 0x31cd86d3, 0x3c8ea00a, 0x384fbdbd,
		0x4c11db70, 0x48d0c6c7, 0x4593e01e, 0x4152fda9,
		0x5f15adac, 0x5bd4b01b, 0x569796c2, 0x52568b75,
		0x6a1936c8, 0x6ed82b7f, 0x639b0da6, 0x675a1011,
		0x791d4014, 0x7ddc5da3, 0x709f7b7a, 0x745e66cd,
		0x9823b6e0, 0x9ce2ab57, 0x91a18d8e, 0x95609039,
		0x8b27c03c, 0x8fe6dd8b, 0x82a5fb52, 0x8664e6e5,
		0xbe2b5b58, 0xbaea46ef, 0xb7a96036, 0xb3687d81,
		0xad2f2d84, 0xa9ee3033, 0xa4ad16ea, 0xa06c0b5d,
		0xd4326d90, 0xd0f37027, 0xddb056fe, 0xd9714b49,
		0xc7361b4c, 0xc3f706fb, 0xceb42022, 0xca753d95,
		0xf23a8028, 0xf6fb9d9f, 0xfbb8bb46, 0xff79a6f1,
		0xe13ef6f4, 0xe5ffeb43, 0xe8bccd9a, 0xec7dd02d,
		0x34867077, 0x30476dc0, 0x3d044b19, 0x39c556ae,
		0x278206ab, 0x23431b1c, 0x2e003dc5, 0x2ac12072,
		0x128e9dcf, 0x164f8078, 0x1b0ca6a1, 0x1fcdbb16,
		0x018aeb13, 0x054bf6a4, 0x0808d07d, 0x0cc9cdca,
		0x7897ab07, 0x7c56b6b0, 0x71159069, 0x75d48dde,
		0x6b93dddb, 0x6f52c06c, 0x6211e6b5, 0x66d0fb02,
		0x5e9f46bf, 0x5a5e5b08, 0x571d7dd1, 0x53dc6066,
		0x4d9b3063, 0x495a2dd4, 0x44190b0d, 0x40d816ba,
		0xaca5c697, 0xa864db20, 0xa527fdf9, 0xa1e6e04e,
		0xbfa1b04b, 0xbb60adfc, 0xb6238b25, 0xb2e29692,
		0x8aad2b2f, 0x8e6c3698, 0x832f1041, 0x87ee0df6,
		0x99a95df3, 0x9d684044, 0x902b669d, 0x94ea7b2a,
		0xe0b41de7, 0xe4750050, 0xe9362689, 0xedf73b3e,
		0xf3b06b3b, 0xf771768c, 0xfa325055, 0xfef34de2,
		0xc6bcf05f, 0xc27dede8, 0xcf3ecb31, 0xcbffd686,
		0xd5b88683, 0xd1799b34, 0xdc3abded, 0xd8fba05a,
		0x690ce0ee, 0x6dcdfd59, 0x608edb80, 0x644fc637,
		0x7a089632, 0x7ec98b85, 0x738aad5c, 0x774bb0eb,
		0x4f040d56, 0x4bc510e1, 0x46863638, 0x42472b8f,
		0x5c007b8a, 0x58c1663d, 0x558240e4, 0x51435d53,
		0x251d3b9e, 0x21dc2629, 0x2c9f00f0, 0x285e1d47,
		0x36194d42, 0x32d850f5, 0x3f9b762c, 0x3b5a6b9b,
		0x0315d626, 0x07d4cb91, 0x0a97ed48, 0x0e56f0ff,
		0x1011a0fa, 0x14d0bd4d, 0x19939b94, 0x1d528623,
		0xf12f560e, 0xf5ee4bb9, 0xf8ad6d60, 0xfc6c70d7,
		0xe22b20d2, 0xe6ea3d65, 0xeba91bbc, 0xef68060b,
		0xd727bbb6, 0xd3e6a601, 0xdea580d8, 0xda649d6f,
		0xc423cd6a, 0xc0e2d0dd, 0xcda1f604, 0xc960ebb3,
		0xbd3e8d7e, 0xb9ff90c9, 0xb4bcb610, 0xb07daba7,
		0xae3afba2, 0xaafbe615, 0xa7b8c0cc, 0xa379dd7b,
		0x9b3660c6, 0x9ff77d71, 0x92b45ba8, 0x9675461f,
		0x8832161a, 0x8cf30bad, 0x81b02d74, 0x857130c3,
		0x5d8a9099, 0x594b8d2e, 0x5408abf7, 0x50c9b640,
		0x4e8ee645, 0x4a4ffbf2, 0x470cdd2b, 0x43cdc09c,
		0x7b827d21, 0x7f436096, 0x7200464f, 0x76c15bf8,
		0x68860bfd, 0x6c47164a, 0x61043093, 0x65c52d24,
		0x119b4be9, 0x155a565e, 0x18197087, 0x1cd86d30,
		0x029f3d35, 0x065e2082, 0x0b1d065b, 0x0fdc1bec,
		0x3793a651, 0x3352bbe6, 0x3e119d3f, 0x3ad08088,
		0x2497d08d, 0x2056cd3a, 0x2d15ebe3, 0x29d4f654,
		0xc5a92679, 0xc1683bce, 0xcc2b1d17, 0xc8ea00a0,
		0xd6ad50a5, 0xd26c4d12, 0xdf2f6bcb, 0xdbee767c,
		0xe3a1cbc1, 0xe760d676, 0xea23f0af, 0xeee2ed18,
		0xf0a5bd1d, 0xf464a0aa, 0xf9278673, 0xfde69bc4,
		0x89b8fd09, 0x8d79e0be, 0x803ac667, 0x84fbdbd0,
		0x9abc8bd5, 0x9e7d9662, 0x933eb0bb, 0x97ffad0c,
		0xafb010b1, 0xab710d06, 0xa6322bdf, 0xa2f33668,
		0xbcb4666d, 0xb8757bda, 0xb5365d03, 0xb1f740b4,
	}

	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ crcTable[((crc>>24)^uint32(b))&0xff]
	}
	return crc
}

func listServerCodecs() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server to retrieve supported codecs: %s\n", whepURL)

	// Create a MediaEngine with all possible codecs
	mediaEngine := &webrtc.MediaEngine{}

	// Register all video codecs
	videoCodecs := []struct {
		mimeType    string
		payloadType uint8
	}{
		{webrtc.MimeTypeH264, 96},
		{webrtc.MimeTypeVP8, 97},
		{webrtc.MimeTypeVP9, 98},
	}

	for _, codec := range videoCodecs {
		if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{
				MimeType: codec.mimeType, ClockRate: 90000,
			},
			PayloadType: webrtc.PayloadType(codec.payloadType),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	// Register audio codec
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return err
	}

	// Create interceptor registry and API
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return err
	}

	api := webrtc.NewAPI(
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	// Create peer connection
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer peerConnection.Close()

	// Add transceivers
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}

	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}

	// Create offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		return err
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	if err = peerConnection.SetLocalDescription(offer); err != nil {
		return err
	}

	<-gatherComplete

	// Send offer to WHEP server
	req, err := http.NewRequest("POST", whepURL, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/sdp")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("WHEP server returned status %d: %s", resp.StatusCode, string(body))
	}

	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  string(answer),
	}); err != nil {
		return err
	}

	// Get negotiated codecs from transceivers
	fmt.Println("\nSupported codecs by WHEP server:")
	fmt.Println("\nVideo codecs:")

	transceivers := peerConnection.GetTransceivers()
	for _, transceiver := range transceivers {
		if transceiver.Kind() == webrtc.RTPCodecTypeVideo {
			codecs := transceiver.Receiver().GetParameters().Codecs
			for _, codec := range codecs {
				fmt.Printf("  - %s (payload type: %d, clock rate: %d)\n",
					codec.MimeType, codec.PayloadType, codec.ClockRate)
			}
		}
	}

	fmt.Println("\nAudio codecs:")
	for _, transceiver := range transceivers {
		if transceiver.Kind() == webrtc.RTPCodecTypeAudio {
			codecs := transceiver.Receiver().GetParameters().Codecs
			for _, codec := range codecs {
				fmt.Printf("  - %s (payload type: %d, clock rate: %d, channels: %d)\n",
					codec.MimeType, codec.PayloadType, codec.ClockRate, codec.Channels)
			}
		}
	}

	return nil
}

// pipeRawStream pipes raw codec data to a writer (typically stdout for ffplay)
func pipeRawStream(track *webrtc.TrackRemote, w io.Writer, codecType string) {
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
				// For H264, we need to handle NAL units properly
				// This is a simplified version - production code should handle:
				// - STAP-A (Single Time Aggregation Packet)
				// - FU-A (Fragmentation Unit)
				// - Parameter sets (SPS/PPS)

				if len(payload) < 1 {
					continue
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
						continue
					}

					fuHeader := payload[1]
					isStart := (fuHeader & 0x80) != 0
					isEnd := (fuHeader & 0x40) != 0

					if isStart {
						// Start of fragmented NAL
						nalBuffer = nil
						// Reconstruct NAL header
						nalHeader := (payload[0] & 0xE0) | (fuHeader & 0x1F)
						nalBuffer = append(nalBuffer, nalHeader)
					}

					// Append fragment data
					if len(payload) > 2 {
						nalBuffer = append(nalBuffer, payload[2:]...)
					}

					if isEnd && len(nalBuffer) > 0 {
						// End of fragmented NAL - write it out
						startCode := []byte{0x00, 0x00, 0x00, 0x01}
						if _, err := w.Write(startCode); err != nil {
							fmt.Fprintf(os.Stderr, "Error writing start code: %v\n", err)
							return
						}

						if _, err := w.Write(nalBuffer); err != nil {
							fmt.Fprintf(os.Stderr, "Error writing NAL unit: %v\n", err)
							return
						}
						nalBuffer = nil
					}
				}
			case "vp8":
				// Write IVF header on first packet
				if !ivfHeaderWritten {
					if err := writeIVFHeader(w, "VP80"); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return
					}
					ivfHeaderWritten = true
					firstTimestamp = rtpPacket.Timestamp
				}

				// Parse VP8 payload
				if len(payload) < 1 {
					continue
				}

				// VP8 payload descriptor
				var vp8Header int
				headerSize := 1

				// X bit check
				if payload[0]&0x80 != 0 {
					headerSize++
					if len(payload) < headerSize {
						continue
					}
					vp8Header = 1
				}

				// Check S bit for start of partition
				isStart := (payload[0] & 0x10) != 0

				// Skip VP8 payload descriptor
				if len(payload) <= vp8Header {
					continue
				}

				payloadData := payload[headerSize:]

				// For VP8, check if this is a keyframe
				if isStart && len(payloadData) >= 3 {
					// VP8 bitstream - check P bit in first byte
					isKeyFrame := (payloadData[0] & 0x01) == 0
					if !seenKeyFrame && !isKeyFrame {
						continue
					}
					seenKeyFrame = true
				}

				// Accumulate frame data
				if isStart {
					currentFrame = nil
				}
				currentFrame = append(currentFrame, payloadData...)

				// Write frame when marker bit is set
				if rtpPacket.Marker && len(currentFrame) > 0 {
					// Write IVF frame header
					frameHeader := make([]byte, 12)
					binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(currentFrame)))
					// Calculate timestamp
					timestamp := (rtpPacket.Timestamp - firstTimestamp) / 90 // Convert from 90kHz to milliseconds
					binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

					if _, err := w.Write(frameHeader); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
						return
					}

					if _, err := w.Write(currentFrame); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing VP8 frame: %v\n", err)
						return
					}

					frameCount++
					currentFrame = nil
				}
			case "vp9":
				// Write IVF header on first packet
				if !ivfHeaderWritten {
					if err := writeIVFHeader(w, "VP90"); err != nil {
						fmt.Fprintf(os.Stderr, "%v\n", err)
						return
					}
					ivfHeaderWritten = true
					firstTimestamp = rtpPacket.Timestamp
				}

				// Parse VP9 payload
				if len(payload) < 1 {
					continue
				}

				// VP9 payload descriptor
				headerSize := 1

				// I bit check (extended picture ID)
				if payload[0]&0x80 != 0 {
					headerSize++
					if len(payload) < headerSize {
						continue
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
					continue
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
					seenKeyFrame = true
				} else if !seenKeyFrame {
					continue
				}

				// Accumulate frame data
				if isStart {
					currentFrame = nil
				}
				currentFrame = append(currentFrame, payloadData...)

				// Write frame when we have end bit or marker
				if (isEnd || rtpPacket.Marker) && len(currentFrame) > 0 {
					// Write IVF frame header
					frameHeader := make([]byte, 12)
					binary.LittleEndian.PutUint32(frameHeader[0:], uint32(len(currentFrame)))
					// Calculate timestamp
					timestamp := (rtpPacket.Timestamp - firstTimestamp) / 90 // Convert from 90kHz to milliseconds
					binary.LittleEndian.PutUint64(frameHeader[4:], uint64(timestamp))

					if _, err := w.Write(frameHeader); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing IVF frame header: %v\n", err)
						return
					}

					if _, err := w.Write(currentFrame); err != nil {
						fmt.Fprintf(os.Stderr, "Error writing VP9 frame: %v\n", err)
						return
					}

					frameCount++
					currentFrame = nil
				}
			default:
				// For other codecs, write raw payload
				if _, err := w.Write(payload); err != nil {
					fmt.Fprintf(os.Stderr, "Error writing video payload: %v\n", err)
					return
				}
			}
		} else {
			// For audio (Opus), write OggOpus container
			if !oggHeaderWritten {
				// Write OggOpus headers
				seq, err := writeOggOpusHeader(w, serialNumber)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error writing OggOpus header: %v\n", err)
					return
				}
				pageSequence = seq
				oggHeaderWritten = true
			}

			// Calculate duration in samples (48kHz)
			// Opus packets in WebRTC are typically 20ms (960 samples @ 48kHz)
			samplesPerPacket := uint64(960) // 20ms @ 48kHz
			granulePosition += samplesPerPacket

			// Write Opus packet as Ogg page
			if err := writeOggPage(w, payload, granulePosition, false, false, false, serialNumber, &pageSequence); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing Ogg page: %v\n", err)
				return
			}
		}
	}
}
