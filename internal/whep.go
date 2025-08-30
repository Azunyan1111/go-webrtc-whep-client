package internal

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

func ExchangeSDPWithWHEP(peerConnection *webrtc.PeerConnection, url string) error {
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
	if DebugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Offer ===\n%s\n=== End Offer ===\n\n", peerConnection.LocalDescription().SDP)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewReader([]byte(peerConnection.LocalDescription().SDP)))
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

	if DebugMode {
		fmt.Fprintf(os.Stderr, "\n=== SDP Answer ===\n%s\n=== End Answer ===\n\n", string(answer))
	}

	return nil
}

func ListServerCodecs() error {
	fmt.Fprintf(os.Stderr, "Connecting to WHEP server to retrieve supported codecs: %s\n", WhepURL)

	// Create a MediaEngine with all possible codecs
	mediaEngine, err := CreateAllCodecsMediaEngine()
	if err != nil {
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

	// Exchange SDP with WHEP server
	if err := ExchangeSDPWithWHEP(peerConnection, WhepURL); err != nil {
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
