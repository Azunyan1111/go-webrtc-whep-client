package main

import (
	"github.com/Azunyan1111/go-webrtc-whep-client/cmd"
	"log"
)

func main() {
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
