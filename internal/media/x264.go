package media

/*
#cgo pkg-config: x264
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <x264.h>

typedef struct {
	x264_t *enc;
	x264_picture_t pic;
	x264_param_t param;
	int64_t pts;
} cenc;

static cenc *cenc_open(int w, int h, int fps, int crf, int maxkbps, int threads) {
	cenc *e = calloc(1, sizeof(cenc));
	if (x264_param_default_preset(&e->param, "ultrafast", "zerolatency") < 0) { free(e); return NULL; }
	e->param.i_width = w;
	e->param.i_height = h;
	e->param.i_csp = X264_CSP_I420;
	e->param.i_fps_num = fps;
	e->param.i_fps_den = 1;
	e->param.i_timebase_num = 1;
	e->param.i_timebase_den = 1000;
	e->param.b_vfr_input = 0;
	e->param.b_repeat_headers = 1;
	e->param.b_annexb = 1;
	e->param.i_keyint_max = X264_KEYINT_MAX_INFINITE;
	e->param.i_threads = threads;
	e->param.b_sliced_threads = threads > 1 ? 1 : 0;
	e->param.i_log_level = X264_LOG_NONE;
	e->param.rc.i_rc_method = X264_RC_CRF;
	e->param.rc.f_rf_constant = crf;
	e->param.rc.i_vbv_max_bitrate = maxkbps;
	e->param.rc.i_vbv_buffer_size = maxkbps / 2;
	e->param.i_level_idc = 42;
	x264_param_apply_profile(&e->param, "baseline");
	e->enc = x264_encoder_open(&e->param);
	if (!e->enc) { free(e); return NULL; }
	if (x264_picture_alloc(&e->pic, X264_CSP_I420, w, h) < 0) { x264_encoder_close(e->enc); free(e); return NULL; }
	return e;
}

static int cenc_reconfig(cenc *e, int crf, int maxkbps) {
	e->param.rc.f_rf_constant = crf;
	e->param.rc.i_vbv_max_bitrate = maxkbps;
	e->param.rc.i_vbv_buffer_size = maxkbps / 2;
	return x264_encoder_reconfig(e->enc, &e->param);
}

static uint8_t *cenc_plane(cenc *e, int i) { return e->pic.img.plane[i]; }
static int cenc_stride(cenc *e, int i) { return e->pic.img.i_stride[i]; }

static int cenc_encode(cenc *e, int64_t pts, int idr, uint8_t *out, int cap, int *key) {
	x264_nal_t *nals; int n; x264_picture_t po;
	e->pic.i_pts = pts;
	e->pic.i_type = idr ? X264_TYPE_IDR : X264_TYPE_AUTO;
	int sz = x264_encoder_encode(e->enc, &nals, &n, &e->pic, &po);
	if (sz <= 0) return sz;
	if (sz > cap) return -2;
	int off = 0;
	for (int i = 0; i < n; i++) { memcpy(out + off, nals[i].p_payload, nals[i].i_payload); off += nals[i].i_payload; }
	*key = po.b_keyframe;
	return off;
}

static void cenc_close(cenc *e) {
	x264_picture_clean(&e->pic);
	x264_encoder_close(e->enc);
	free(e);
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

type H264Options struct {
	Width   int
	Height  int
	FPS     int
	CRF     int
	MaxKbps int
	Threads int
}

type H264Encoder struct {
	mu   sync.Mutex
	e    *C.cenc
	w, h int
	out  []byte
}

func (o H264Options) withDefaults() H264Options {
	if o.FPS == 0 {
		o.FPS = 30
	}
	if o.CRF == 0 {
		o.CRF = 25
	}
	if o.MaxKbps == 0 {
		o.MaxKbps = 6000
	}
	if o.Threads == 0 {
		o.Threads = 2
	}
	return o
}

func NewH264(o H264Options) (*H264Encoder, error) {
	o = o.withDefaults()
	w, h := o.Width&^1, o.Height&^1
	e := C.cenc_open(C.int(w), C.int(h), C.int(o.FPS), C.int(o.CRF), C.int(o.MaxKbps), C.int(o.Threads))
	if e == nil {
		return nil, errors.New("x264 open failed")
	}
	return &H264Encoder{e: e, w: w, h: h, out: make([]byte, w*h*2+65536)}, nil
}

func (x *H264Encoder) Name() string { return "x264" }

func (x *H264Encoder) Size() (int, int) { return x.w, x.h }

func (x *H264Encoder) Reconfigure(crf, maxKbps int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if C.cenc_reconfig(x.e, C.int(crf), C.int(maxKbps)) < 0 {
		return errors.New("reconfig failed")
	}
	return nil
}

func (x *H264Encoder) Encode(bgrx []byte, stride, srcW, srcH int, ptsMs int64, idr bool) ([]byte, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e == nil {
		return nil, false, errors.New("closed")
	}
	yP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 0))), int(C.cenc_stride(x.e, 0))*x.h)
	uP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 1))), int(C.cenc_stride(x.e, 1))*x.h/2)
	vP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 2))), int(C.cenc_stride(x.e, 2))*x.h/2)
	BGRXToI420(bgrx, stride, srcW, srcH, yP, int(C.cenc_stride(x.e, 0)), uP, vP, int(C.cenc_stride(x.e, 1)), x.w, x.h)
	var key C.int
	idrI := 0
	if idr {
		idrI = 1
	}
	n := C.cenc_encode(x.e, C.int64_t(ptsMs), C.int(idrI), (*C.uint8_t)(unsafe.Pointer(&x.out[0])), C.int(len(x.out)), &key)
	if n < 0 {
		return nil, false, errors.New("encode failed")
	}
	if n == 0 {
		return nil, false, nil
	}
	b := make([]byte, int(n))
	copy(b, x.out[:n])
	return b, key != 0, nil
}

func (x *H264Encoder) EncodeRect(src []byte, stride, rx, ry, rw, rh int, ptsMs int64, idr bool) ([]byte, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e == nil {
		return nil, false, errors.New("closed")
	}
	ys := int(C.cenc_stride(x.e, 0))
	uvs := int(C.cenc_stride(x.e, 1))
	if rw > 0 && rh > 0 && rx >= 0 && ry >= 0 && rx+rw <= x.w && ry+rh <= x.h && rx%2 == 0 && ry%2 == 0 && rw%2 == 0 && rh%2 == 0 {
		yP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 0))), ys*x.h)
		uP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 1))), uvs*x.h/2)
		vP := unsafe.Slice((*byte)(unsafe.Pointer(C.cenc_plane(x.e, 2))), uvs*x.h/2)
		BGRXRectToI420(src, stride, rx, ry, rw, rh, yP, ys, uP, vP, uvs)
	}
	var key C.int
	idrI := 0
	if idr {
		idrI = 1
	}
	n := C.cenc_encode(x.e, C.int64_t(ptsMs), C.int(idrI), (*C.uint8_t)(unsafe.Pointer(&x.out[0])), C.int(len(x.out)), &key)
	if n < 0 {
		return nil, false, errors.New("encode failed")
	}
	if n == 0 {
		return nil, false, nil
	}
	b := make([]byte, int(n))
	copy(b, x.out[:n])
	return b, key != 0, nil
}

func (x *H264Encoder) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e != nil {
		C.cenc_close(x.e)
		x.e = nil
	}
}
