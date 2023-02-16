package main

import (
	"log"
	"net/http"

	"github.com/jmaralo/rtp-to-webrtc-broadcast/broadcast"
)

func main() {
	broadcastHandle, err := broadcast.NewBroadcastHandle("192.168.1.3:6969", "video", "cam1", 1600)
	if err != nil {
		log.Println(err)
	}

	http.Handle("/signal", broadcastHandle)
	//http.Handle("/", http.FileServer(http.Dir("./build")))
	log.Println(http.ListenAndServe("192.168.1.3:4050", nil))
}
