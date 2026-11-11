package stream

import (
	"context"
	"errors"
	"net"
	"sync"
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
	closed chan struct{}
	once   sync.Once
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
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}, "video", "agora")
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
	s.Sink = NewQueueSink(id, mode, 6, func(f *Frame) error {
		return track.WriteSample(media.Sample{Data: f.Data, Duration: f.Duration})
	})
	s.Sink.OnError = func(error) { pc.Close() }
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) { s.stateChanged(st, h.OnClose) })
	fail := func(err error) (*RTCSession, string, error) {
		s.Sink.Close()
		pc.Close()
		return nil, "", err
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return fail(err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(err)
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		return fail(err)
	}
	select {
	case <-gather:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	ld := pc.LocalDescription()
	if ld == nil {
		return fail(errors.New("no local description"))
	}
	return s, ld.SDP, nil
}

func (s *RTCSession) stateChanged(st webrtc.PeerConnectionState, onClose func()) {
	if st == webrtc.PeerConnectionStateConnected && s.Sink != nil {
		s.Sink.RequestKey()
		return
	}
	if st != webrtc.PeerConnectionStateFailed && st != webrtc.PeerConnectionStateClosed {
		return
	}
	s.once.Do(func() {
		close(s.closed)
		if onClose != nil {
			onClose()
		}
	})
}

func (s *RTCSession) Closed() <-chan struct{} { return s.closed }

func (s *RTCSession) Close() {
	s.Sink.Close()
	s.PC.Close()
}
