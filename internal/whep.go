package internal

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

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
