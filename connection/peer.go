package connection

import (
	"encoding/json"
	"log"

	"github.com/jmaralo/rtp-to-webrtc-broadcast/signal"
	"github.com/pion/webrtc/v3"
)

type RemotePeer struct {
	signal *signal.SignalHandle
	peer   *webrtc.PeerConnection
	track  *webrtc.TrackLocalStaticRTP
	stream <-chan []byte
	close  chan<- string
	data   *PeerData
}

func newRemotePeer(sig *signal.SignalHandle, stream <-chan []byte, close chan<- string, data *PeerData) (*RemotePeer, error) {
	log.Println("New Peer")
	peer, err := data.api.NewPeerConnection(data.config)
	remotePeer := &RemotePeer{
		signal: sig,
		peer:   peer,
		track:  nil,
		stream: stream,
		close:  close,
		data:   data,
	}

	if err != nil {
		remotePeer.Close("failed to create peer connection")
		return nil, err
	}

	remotePeer.signal.SetEvent("offer", remotePeer.onSignalOffer)
	remotePeer.signal.SetEvent("reject", remotePeer.onSignalReject)
	remotePeer.signal.SetEvent("close", remotePeer.onSignalClose)
	remotePeer.signal.SetEvent("answer", remotePeer.onSignalAnswer)
	remotePeer.signal.SetEvent("candidate", remotePeer.onSignalCandidate)

	remotePeer.peer.OnICECandidate(remotePeer.onICECandidate)
	remotePeer.peer.OnConnectionStateChange(remotePeer.onConnectionStateChange)

	remotePeer.addTrack()
	remotePeer.beginHandshake()

	return remotePeer, nil
}

func (peer *RemotePeer) onSignalOffer(msg json.RawMessage) {
	peer.signal.SendMessage("reject", "unexpected offer")
	peer.Close("misbehaving host")
}

func (peer *RemotePeer) onSignalReject(msg json.RawMessage) {
	log.Printf("Peer rejected signal with message %s\n", string(msg))
}

func (peer *RemotePeer) onSignalClose(msg json.RawMessage) {
	log.Printf("Remote peer requested close with message: %s\n", string(msg))
	peer.Close("ok")
}

func (peer *RemotePeer) onSignalAnswer(msg json.RawMessage) {
	ans := new(string)
	if err := json.Unmarshal(msg, ans); err != nil {
		log.Printf("RemotePeer: onAnswer: Unmarshal: %s\n", err)
		peer.signal.SendMessage("reject", "invalid answer")
		peer.Close("failed to parse answer")
		return
	}

	err := peer.peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: *ans})
	if err != nil {
		log.Printf("RemotePeer: onAnswer: SetRemoteDescription: %s\n", err)
		peer.Close("failed to add answer")
		return
	}
}

func (peer *RemotePeer) onSignalCandidate(msg json.RawMessage) {
	candidate := new(webrtc.ICECandidateInit)
	if err := json.Unmarshal(msg, candidate); err != nil {
		log.Printf("RemotePeer: onCandidate: Unmarshal: %s\n", err)
		peer.signal.SendMessage("reject", "invalid candidate")
		peer.Close("failed to parse candidate")
		return
	}

	err := peer.peer.AddICECandidate(*candidate)
	if err != nil {
		log.Printf("RemotePeer: onCandidate: AddICECandidate: %s\n", err)
		peer.Close("failed to add candidate")
		return
	}
}

func (peer *RemotePeer) onICECandidate(candidate *webrtc.ICECandidate) {
	if candidate == nil {
		return
	}
	if err := peer.signal.SendMessage("candidate", candidate.ToJSON()); err != nil {
		peer.Close("failed to send candidate")
		return
	}
}

func (peer *RemotePeer) onConnectionStateChange(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		if !peer.data.running {
			go peer.run()
		}
	default:
	}
}

func (peer *RemotePeer) run() {
	peer.data.running = true
	defer func() { peer.data.running = false }()
	for peer.track == nil {
	}
	for {
		data, ok := <-peer.stream
		if !ok {
			log.Println("RemotePeer: run: channel closed")
			peer.Close("failed to read stream")
			return
		}
		if _, err := peer.track.Write(data); err != nil {
			log.Printf("RemotePeer: run: Write: %s\n", err)
			peer.Close("failed to write track")
			return
		}
	}
}

func (peer *RemotePeer) addTrack() {
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, peer.data.trackID, peer.data.streamID)
	if err != nil {
		log.Printf("RemotePeer: addTrack: NewTrackLocalStaticRTP: %s\n", err)
		peer.Close("failed to create track")
		return
	}

	peer.track = track
	rtpSender, err := peer.peer.AddTrack(track)
	if err != nil {
		log.Printf("RemotePeer: addTrack: AddTrack: %s\n", err)
		peer.Close("failed to add track")
		return
	}

	rtpSenderBuf := make([]byte, peer.data.mtu)
	go func() {
		for {
			if _, _, err := rtpSender.Read(rtpSenderBuf); err != nil {
				log.Printf("RemotePeer: RTPSender: Read: %s\n", err)
				return
			}
		}
	}()
}

func (peer *RemotePeer) beginHandshake() {
	go peer.signal.Listen()

	offer, err := peer.peer.CreateOffer(nil)
	if err != nil {
		log.Printf("RemotePeer: beginHandshake: CreateOffer: %s\n", err)
		peer.Close("failed to create offer")
		return
	}

	if err := peer.peer.SetLocalDescription(offer); err != nil {
		log.Printf("RemotePeer: beginHandshake: SetLocalDescription: %s\n", err)
		peer.Close("failed to add offer")
		return
	}

	if err := peer.signal.SendMessage("offer", offer.SDP); err != nil {
		log.Printf("RemotePeer: beginHandshake: SendMessage: %s\n", err)
		peer.Close("failed to send offer")
		return
	}
}

func (peer *RemotePeer) Close(reason string) {
	if peer.data.closed {
		return
	}

	log.Printf("Closing peer connection, reason: %s\n", reason)
	peer.signal.SendMessage("close", reason)
	peer.signal.Close()
	peer.peer.Close()
	peer.close <- peer.data.id
	peer.data.closed = true
}
