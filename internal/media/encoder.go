package media

import (
	"errors"
	"log"
	"os"
	"sync/atomic"
)

type Encoder interface {
	Name() string
	Reconfigure(crf, maxKbps, bufKbps int) error
	Encode(bgrx []byte, stride, srcW, srcH int, ptsMs int64, idr bool) ([]byte, bool, error)
	EncodeRect(src []byte, stride, rx, ry, rw, rh int, ptsMs int64, idr bool) ([]byte, bool, error)
	Close()
}

var vaapiBroken atomic.Bool

func EncoderPref() string {
	switch p := os.Getenv("AGORA_ENCODER"); p {
	case "x264", "vaapi":
		return p
	default:
		return "auto"
	}
}

func NewEncoder(o H264Options) (Encoder, error) {
	pref := EncoderPref()
	if pref == "vaapi" || (pref == "auto" && !vaapiBroken.Load()) {
		e, err := NewVAAPI(o)
		if err == nil {
			return e, nil
		}
		if pref == "vaapi" {
			return nil, err
		}
		var oe *vaapiOpenError
		if errors.As(err, &oe) && !oe.unsupported() {
			log.Printf("encoder: %v, using x264 for this stream", err)
		} else if !vaapiBroken.Swap(true) {
			log.Printf("encoder: %v, using x264", err)
		}
	}
	return NewH264(o)
}

func EncoderFailed(e Encoder, err error) {
	if errors.Is(err, ErrShortFrame) {
		return
	}
	if e.Name() == "vaapi" && EncoderPref() == "auto" && !vaapiBroken.Swap(true) {
		log.Printf("encoder: %v, falling back to x264", err)
	}
}
