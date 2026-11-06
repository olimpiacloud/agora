package stream

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type RTC struct {
	api *webrtc.API
}

type RTCOptions struct {
	UDPPort     int
	IncludeLoop bool
	PublicIPs   []string
	Interfaces  []string
}

func NewRTC(o RTCOptions) (*RTC, error) {
	m := &webrtc.MediaEngine{}
	err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}},
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo)
	if err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(o.IncludeLoop)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	if len(o.Interfaces) > 0 {
		allow := map[string]bool{}
		for _, i := range o.Interfaces {
			allow[i] = true
		}
		se.SetInterfaceFilter(func(n string) bool { return allow[n] || n == "lo" && o.IncludeLoop })
	}
	if len(o.PublicIPs) > 0 {
		se.SetNAT1To1IPs(o.PublicIPs, webrtc.ICECandidateTypeHost)
	}
	if o.UDPPort > 0 {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: o.UDPPort})
		if err != nil {
			return nil, err
		}
		se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, conn))
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return &RTC{api: api}, nil
}

type RTCSession struct {
	PC     *webrtc.PeerConnection
	Track  *webrtc.TrackLocalStaticSample
	Sink   *QueueSink
	Events chan []byte
	closed chan struct{}
}

type RTCHandlers struct {
	OnInput    func([]byte)
	OnKeyframe func()
	OnClose    func()
	OnEventsDC func(send func([]byte) error)
}

func (r *RTC) Answer(ctx context.Context, id string, mode Mode, offer string, h RTCHandlers) (*RTCSession, string, error) {
	pc, err := r.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, "", err
	}
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}, "video", "carbon")
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	s := &RTCSession{PC: pc, Track: track, closed: make(chan struct{})}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, _, err := sender.Read(buf)
			if err != nil {
				return
			}
			pkts, err := rtcp.Unmarshal(buf[:n])
			if err != nil {
				continue
			}
			for _, p := range pkts {
				switch p.(type) {
				case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
					if h.OnKeyframe != nil {
						h.OnKeyframe()
					}
				}
			}
		}
	}()
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		switch dc.Label() {
		case "input":
			dc.OnMessage(func(m webrtc.DataChannelMessage) {
				if h.OnInput != nil {
					h.OnInput(m.Data)
				}
			})
		case "events":
			dc.OnOpen(func() {
				if h.OnEventsDC != nil {
					h.OnEventsDC(func(b []byte) error { return dc.SendText(string(b)) })
				}
			})
		}
	})
	var closeOnce bool
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if (st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed || st == webrtc.PeerConnectionStateDisconnected) && !closeOnce {
			closeOnce = true
			close(s.closed)
			if h.OnClose != nil {
				h.OnClose()
			}
		}
	})
	s.Sink = NewQueueSink(id, mode, 6, func(f *Frame) error {
		return track.WriteSample(media.Sample{Data: f.Data, Duration: f.Duration})
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		pc.Close()
		return nil, "", err
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		pc.Close()
		return nil, "", err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		pc.Close()
		return nil, "", err
	}
	select {
	case <-gather:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		pc.Close()
		return nil, "", ctx.Err()
	}
	ld := pc.LocalDescription()
	if ld == nil {
		pc.Close()
		return nil, "", errors.New("no local description")
	}
	return s, ld.SDP, nil
}

func (s *RTCSession) Closed() <-chan struct{} { return s.closed }

func (s *RTCSession) Close() {
	s.Sink.Close()
	s.PC.Close()
}
