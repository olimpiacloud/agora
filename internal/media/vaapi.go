package media

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <va/va.h>
#include <va/va_drm.h>
#include <va/va_enc_h264.h>

static __typeof__(&vaGetDisplayDRM) p_vaGetDisplayDRM;
#define vaGetDisplayDRM p_vaGetDisplayDRM
static __typeof__(&vaSetErrorCallback) p_vaSetErrorCallback;
#define vaSetErrorCallback p_vaSetErrorCallback
static __typeof__(&vaSetInfoCallback) p_vaSetInfoCallback;
#define vaSetInfoCallback p_vaSetInfoCallback
static __typeof__(&vaInitialize) p_vaInitialize;
#define vaInitialize p_vaInitialize
static __typeof__(&vaTerminate) p_vaTerminate;
#define vaTerminate p_vaTerminate
static __typeof__(&vaGetConfigAttributes) p_vaGetConfigAttributes;
#define vaGetConfigAttributes p_vaGetConfigAttributes
static __typeof__(&vaCreateConfig) p_vaCreateConfig;
#define vaCreateConfig p_vaCreateConfig
static __typeof__(&vaDestroyConfig) p_vaDestroyConfig;
#define vaDestroyConfig p_vaDestroyConfig
static __typeof__(&vaCreateSurfaces) p_vaCreateSurfaces;
#define vaCreateSurfaces p_vaCreateSurfaces
static __typeof__(&vaDestroySurfaces) p_vaDestroySurfaces;
#define vaDestroySurfaces p_vaDestroySurfaces
static __typeof__(&vaCreateContext) p_vaCreateContext;
#define vaCreateContext p_vaCreateContext
static __typeof__(&vaDestroyContext) p_vaDestroyContext;
#define vaDestroyContext p_vaDestroyContext
static __typeof__(&vaCreateBuffer) p_vaCreateBuffer;
#define vaCreateBuffer p_vaCreateBuffer
static __typeof__(&vaDestroyBuffer) p_vaDestroyBuffer;
#define vaDestroyBuffer p_vaDestroyBuffer
static __typeof__(&vaMapBuffer) p_vaMapBuffer;
#define vaMapBuffer p_vaMapBuffer
static __typeof__(&vaUnmapBuffer) p_vaUnmapBuffer;
#define vaUnmapBuffer p_vaUnmapBuffer
static __typeof__(&vaDeriveImage) p_vaDeriveImage;
#define vaDeriveImage p_vaDeriveImage
static __typeof__(&vaCreateImage) p_vaCreateImage;
#define vaCreateImage p_vaCreateImage
static __typeof__(&vaDestroyImage) p_vaDestroyImage;
#define vaDestroyImage p_vaDestroyImage
static __typeof__(&vaPutImage) p_vaPutImage;
#define vaPutImage p_vaPutImage
static __typeof__(&vaBeginPicture) p_vaBeginPicture;
#define vaBeginPicture p_vaBeginPicture
static __typeof__(&vaRenderPicture) p_vaRenderPicture;
#define vaRenderPicture p_vaRenderPicture
static __typeof__(&vaEndPicture) p_vaEndPicture;
#define vaEndPicture p_vaEndPicture
static __typeof__(&vaSyncSurface) p_vaSyncSurface;
#define vaSyncSurface p_vaSyncSurface

static int va_loaded;
static pthread_once_t va_once = PTHREAD_ONCE_INIT;

static void va_load_once(void) {
	void *va = dlopen("libva.so.2", RTLD_NOW | RTLD_LOCAL);
	void *drm = dlopen("libva-drm.so.2", RTLD_NOW | RTLD_LOCAL);
	if (!va || !drm) return;
	if (!(p_vaGetDisplayDRM = dlsym(drm, "vaGetDisplayDRM"))) return;
	if (!(p_vaSetErrorCallback = dlsym(va, "vaSetErrorCallback"))) return;
	if (!(p_vaSetInfoCallback = dlsym(va, "vaSetInfoCallback"))) return;
	if (!(p_vaInitialize = dlsym(va, "vaInitialize"))) return;
	if (!(p_vaTerminate = dlsym(va, "vaTerminate"))) return;
	if (!(p_vaGetConfigAttributes = dlsym(va, "vaGetConfigAttributes"))) return;
	if (!(p_vaCreateConfig = dlsym(va, "vaCreateConfig"))) return;
	if (!(p_vaDestroyConfig = dlsym(va, "vaDestroyConfig"))) return;
	if (!(p_vaCreateSurfaces = dlsym(va, "vaCreateSurfaces"))) return;
	if (!(p_vaDestroySurfaces = dlsym(va, "vaDestroySurfaces"))) return;
	if (!(p_vaCreateContext = dlsym(va, "vaCreateContext"))) return;
	if (!(p_vaDestroyContext = dlsym(va, "vaDestroyContext"))) return;
	if (!(p_vaCreateBuffer = dlsym(va, "vaCreateBuffer"))) return;
	if (!(p_vaDestroyBuffer = dlsym(va, "vaDestroyBuffer"))) return;
	if (!(p_vaMapBuffer = dlsym(va, "vaMapBuffer"))) return;
	if (!(p_vaUnmapBuffer = dlsym(va, "vaUnmapBuffer"))) return;
	if (!(p_vaDeriveImage = dlsym(va, "vaDeriveImage"))) return;
	if (!(p_vaCreateImage = dlsym(va, "vaCreateImage"))) return;
	if (!(p_vaDestroyImage = dlsym(va, "vaDestroyImage"))) return;
	if (!(p_vaPutImage = dlsym(va, "vaPutImage"))) return;
	if (!(p_vaBeginPicture = dlsym(va, "vaBeginPicture"))) return;
	if (!(p_vaRenderPicture = dlsym(va, "vaRenderPicture"))) return;
	if (!(p_vaEndPicture = dlsym(va, "vaEndPicture"))) return;
	if (!(p_vaSyncSurface = dlsym(va, "vaSyncSurface"))) return;
	va_loaded = 1;
}

static int va_load(void) {
	pthread_once(&va_once, va_load_once);
	return va_loaded;
}

#define LOG2_MAX_FRAME_NUM 8

typedef struct {
	int fd;
	VADisplay dpy;
	VAConfigID cfg;
	VAContextID ctx;
	VASurfaceID src;
	VASurfaceID rec[2];
	VABufferID coded;
	VAImage img;
	int derived;
	int have_img;
	int vpp;
	VAConfigID vcfg;
	VAContextID vctx;
	VASurfaceID rgb;
	VAImage rimg;
	int rgb_dirty;
	int w, h, mbw, mbh;
	int fps, kbps, minqp;
	int rc_dirty;
	uint32_t frame_num;
	uint32_t poc;
	int cur;
	int have_ref;
	uint32_t ref_frame_num;
	uint32_t ref_poc;
	uint16_t idr_id;
	int coded_size;
} venc;

static void venc_vpp_close(venc *e) {
	if (e->vpp) vaDestroyImage(e->dpy, e->rimg.image_id);
	if (e->vctx != VA_INVALID_ID) vaDestroyContext(e->dpy, e->vctx);
	if (e->rgb != VA_INVALID_SURFACE) vaDestroySurfaces(e->dpy, &e->rgb, 1);
	if (e->vcfg != VA_INVALID_ID) vaDestroyConfig(e->dpy, e->vcfg);
	e->vpp = 0;
	e->vctx = e->vcfg = VA_INVALID_ID;
	e->rgb = VA_INVALID_SURFACE;
}

static void venc_vpp_open(venc *e, int aw, int ah) {
	if (vaCreateConfig(e->dpy, VAProfileNone, VAEntrypointVideoProc, NULL, 0, &e->vcfg) != VA_STATUS_SUCCESS) { e->vcfg = VA_INVALID_ID; return; }
	VASurfaceAttrib sa;
	memset(&sa, 0, sizeof(sa));
	sa.type = VASurfaceAttribPixelFormat;
	sa.flags = VA_SURFACE_ATTRIB_SETTABLE;
	sa.value.type = VAGenericValueTypeInteger;
	sa.value.value.i = VA_FOURCC_BGRX;
	if (vaCreateSurfaces(e->dpy, VA_RT_FORMAT_RGB32, aw, ah, &e->rgb, 1, &sa, 1) != VA_STATUS_SUCCESS) { e->rgb = VA_INVALID_SURFACE; venc_vpp_close(e); return; }
	if (vaCreateContext(e->dpy, e->vcfg, aw, ah, VA_PROGRESSIVE, &e->src, 1, &e->vctx) != VA_STATUS_SUCCESS) { e->vctx = VA_INVALID_ID; venc_vpp_close(e); return; }
	if (vaDeriveImage(e->dpy, e->rgb, &e->rimg) != VA_STATUS_SUCCESS) { venc_vpp_close(e); return; }
	if (e->rimg.format.fourcc != VA_FOURCC_BGRX && e->rimg.format.fourcc != VA_FOURCC_BGRA) { vaDestroyImage(e->dpy, e->rimg.image_id); venc_vpp_close(e); return; }
	e->vpp = 1;
}

static int venc_map_rgb(venc *e, uint8_t **p, int *stride) {
	void *m;
	if (vaMapBuffer(e->dpy, e->rimg.buf, &m) != VA_STATUS_SUCCESS) return -1;
	*p = (uint8_t *)m + e->rimg.offsets[0];
	*stride = e->rimg.pitches[0];
	return 0;
}

static int venc_unmap_rgb(venc *e) {
	if (vaUnmapBuffer(e->dpy, e->rimg.buf) != VA_STATUS_SUCCESS) return -1;
	e->rgb_dirty = 1;
	return 0;
}

static int venc_convert(venc *e) {
	if (!e->vpp || !e->rgb_dirty) return 0;
	VARectangle r = {0, 0, e->w, e->h};
	VAProcPipelineParameterBuffer pp;
	memset(&pp, 0, sizeof(pp));
	pp.surface = e->rgb;
	pp.surface_region = &r;
	pp.output_region = &r;
	pp.surface_color_standard = VAProcColorStandardBT601;
	pp.output_color_standard = VAProcColorStandardBT601;
	pp.input_color_properties.color_range = VA_SOURCE_RANGE_FULL;
	pp.output_color_properties.color_range = VA_SOURCE_RANGE_REDUCED;
	pp.filter_flags = VA_FILTER_SCALING_FAST;
	VABufferID id;
	if (vaCreateBuffer(e->dpy, e->vctx, VAProcPipelineParameterBufferType, sizeof(pp), 1, &pp, &id) != VA_STATUS_SUCCESS) return -1;
	int r2 = 0;
	if (vaBeginPicture(e->dpy, e->vctx, e->src) != VA_STATUS_SUCCESS) r2 = -2;
	else {
		if (vaRenderPicture(e->dpy, e->vctx, &id, 1) != VA_STATUS_SUCCESS) r2 = -3;
		if (vaEndPicture(e->dpy, e->vctx) != VA_STATUS_SUCCESS) r2 = -4;
	}
	vaDestroyBuffer(e->dpy, id);
	if (r2 == 0 && vaSyncSurface(e->dpy, e->src) != VA_STATUS_SUCCESS) r2 = -5;
	if (r2 == 0) e->rgb_dirty = 0;
	return r2;
}

static void venc_close(venc *e) {
	if (!e) return;
	if (e->dpy) {
		if (e->have_img) vaDestroyImage(e->dpy, e->img.image_id);
		venc_vpp_close(e);
		if (e->coded != VA_INVALID_ID) vaDestroyBuffer(e->dpy, e->coded);
		if (e->ctx != VA_INVALID_ID) vaDestroyContext(e->dpy, e->ctx);
		if (e->src != VA_INVALID_SURFACE) vaDestroySurfaces(e->dpy, &e->src, 1);
		if (e->rec[0] != VA_INVALID_SURFACE) vaDestroySurfaces(e->dpy, e->rec, 2);
		if (e->cfg != VA_INVALID_ID) vaDestroyConfig(e->dpy, e->cfg);
		vaTerminate(e->dpy);
	}
	if (e->fd >= 0) close(e->fd);
	free(e);
}

typedef struct { uint8_t b[256]; int bits; } bw;

static void bw_u(bw *w, int n, uint32_t v) {
	for (int i = n - 1; i >= 0; i--) {
		if (w->bits >= 2000) return;
		if ((v >> i) & 1) w->b[w->bits >> 3] |= 0x80 >> (w->bits & 7);
		w->bits++;
	}
}

static void bw_ue(bw *w, uint32_t v) {
	uint32_t x = v + 1;
	int n = 0;
	while ((x >> n) > 1) n++;
	bw_u(w, n, 0);
	bw_u(w, n + 1, x);
}

static void bw_se(bw *w, int v) { bw_ue(w, v <= 0 ? -2 * v : 2 * v - 1); }

static void bw_trailing(bw *w) {
	bw_u(w, 1, 1);
	while (w->bits & 7) bw_u(w, 1, 0);
}

static int bw_nal(bw *w, uint8_t *out) {
	int n = 0, zeros = 0, bytes = (w->bits + 7) >> 3;
	out[n++] = 0; out[n++] = 0; out[n++] = 0; out[n++] = 1;
	for (int i = 0; i < bytes; i++) {
		if (i > 0 && zeros >= 2 && w->b[i] <= 3) { out[n++] = 3; zeros = 0; }
		out[n++] = w->b[i];
		zeros = w->b[i] == 0 ? zeros + 1 : 0;
	}
	return n;
}

static int add_packed(venc *e, int type, bw *w, int partial, VABufferID *ids, int *n) {
	uint8_t data[300];
	int len = bw_nal(w, data);
	VAEncPackedHeaderParameterBuffer p;
	memset(&p, 0, sizeof(p));
	p.type = type;
	p.bit_length = partial ? (len - ((w->bits + 7) >> 3)) * 8 + w->bits : len * 8;
	p.has_emulation_bytes = 1;
	if (vaCreateBuffer(e->dpy, e->ctx, VAEncPackedHeaderParameterBufferType, sizeof(p), 1, &p, &ids[*n]) != VA_STATUS_SUCCESS) return -1;
	(*n)++;
	if (vaCreateBuffer(e->dpy, e->ctx, VAEncPackedHeaderDataBufferType, len, 1, data, &ids[*n]) != VA_STATUS_SUCCESS) return -1;
	(*n)++;
	return 0;
}

static int level_idc(venc *e) { return e->mbw * e->mbh > 8704 ? 51 : 42; }

static void write_sps(venc *e, bw *w) {
	bw_u(w, 8, 0x67);
	bw_u(w, 8, 66);
	bw_u(w, 8, 0xc0);
	bw_u(w, 8, level_idc(e));
	bw_ue(w, 0);
	bw_ue(w, LOG2_MAX_FRAME_NUM - 4);
	bw_ue(w, 2);
	bw_ue(w, 1);
	bw_u(w, 1, 0);
	bw_ue(w, e->mbw - 1);
	bw_ue(w, e->mbh - 1);
	bw_u(w, 1, 1);
	bw_u(w, 1, 1);
	int crop = e->mbw * 16 != e->w || e->mbh * 16 != e->h;
	bw_u(w, 1, crop);
	if (crop) {
		bw_ue(w, 0);
		bw_ue(w, (e->mbw * 16 - e->w) / 2);
		bw_ue(w, 0);
		bw_ue(w, (e->mbh * 16 - e->h) / 2);
	}
	bw_u(w, 1, 1);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 1);
	bw_u(w, 32, 1);
	bw_u(w, 32, e->fps * 2);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 1);
	bw_u(w, 1, 1);
	bw_ue(w, 0);
	bw_ue(w, 0);
	bw_ue(w, 15);
	bw_ue(w, 15);
	bw_ue(w, 0);
	bw_ue(w, 1);
	bw_trailing(w);
}

static void write_pps(bw *w) {
	bw_u(w, 8, 0x68);
	bw_ue(w, 0);
	bw_ue(w, 0);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_ue(w, 0);
	bw_ue(w, 0);
	bw_ue(w, 0);
	bw_u(w, 1, 0);
	bw_u(w, 2, 0);
	bw_se(w, 0);
	bw_se(w, 0);
	bw_se(w, 0);
	bw_u(w, 1, 1);
	bw_u(w, 1, 0);
	bw_u(w, 1, 0);
	bw_trailing(w);
}

static void write_slice_header(venc *e, bw *w, int idr) {
	bw_u(w, 8, idr ? 0x65 : 0x41);
	bw_ue(w, 0);
	bw_ue(w, idr ? 7 : 5);
	bw_ue(w, 0);
	bw_u(w, LOG2_MAX_FRAME_NUM, e->frame_num);
	if (idr) bw_ue(w, e->idr_id);
	if (!idr) {
		bw_u(w, 1, 0);
		bw_u(w, 1, 0);
	}
	if (idr) {
		bw_u(w, 1, 0);
		bw_u(w, 1, 0);
	} else {
		bw_u(w, 1, 0);
	}
	bw_se(w, 0);
	bw_ue(w, 0);
	bw_se(w, 0);
	bw_se(w, 0);
}

static void venc_silent(void *ctx, const char *msg) {}

static int venc_open(const char *dev, int w, int h, int fps, int kbps, int minqp, int vpp, venc **out) {
	if (!va_load()) return -1;
	venc *e = calloc(1, sizeof(venc));
	e->fd = -1;
	e->cfg = e->ctx = e->coded = VA_INVALID_ID;
	e->src = e->rec[0] = e->rec[1] = VA_INVALID_SURFACE;
	e->vcfg = e->vctx = VA_INVALID_ID;
	e->rgb = VA_INVALID_SURFACE;
	e->w = w; e->h = h;
	e->mbw = (w + 15) / 16; e->mbh = (h + 15) / 16;
	e->fps = fps; e->kbps = kbps; e->minqp = minqp;
	e->fd = open(dev, O_RDWR | O_CLOEXEC);
	if (e->fd < 0) { venc_close(e); return -1; }
	e->dpy = vaGetDisplayDRM(e->fd);
	if (!e->dpy) { venc_close(e); return -2; }
	vaSetErrorCallback(e->dpy, venc_silent, NULL);
	vaSetInfoCallback(e->dpy, venc_silent, NULL);
	int maj, min;
	VAStatus s = vaInitialize(e->dpy, &maj, &min);
	if (s != VA_STATUS_SUCCESS) { vaTerminate(e->dpy); e->dpy = NULL; venc_close(e); return -3; }
	VAConfigAttrib a[3];
	a[0].type = VAConfigAttribRTFormat; a[0].value = VA_RT_FORMAT_YUV420;
	a[1].type = VAConfigAttribRateControl; a[1].value = VA_RC_VBR;
	a[2].type = VAConfigAttribEncPackedHeaders;
	vaGetConfigAttributes(e->dpy, VAProfileH264ConstrainedBaseline, VAEntrypointEncSlice, &a[2], 1);
	int want = VA_ENC_PACKED_HEADER_SEQUENCE | VA_ENC_PACKED_HEADER_PICTURE | VA_ENC_PACKED_HEADER_SLICE;
	if (a[2].value == VA_ATTRIB_NOT_SUPPORTED || (a[2].value & want) != want) { venc_close(e); return -4; }
	a[2].value = want;
	s = vaCreateConfig(e->dpy, VAProfileH264ConstrainedBaseline, VAEntrypointEncSlice, a, 3, &e->cfg);
	if (s != VA_STATUS_SUCCESS) { e->cfg = VA_INVALID_ID; venc_close(e); return -4; }
	int aw = e->mbw * 16, ah = e->mbh * 16;
	s = vaCreateSurfaces(e->dpy, VA_RT_FORMAT_YUV420, aw, ah, &e->src, 1, NULL, 0);
	if (s != VA_STATUS_SUCCESS) { e->src = VA_INVALID_SURFACE; venc_close(e); return -5; }
	s = vaCreateSurfaces(e->dpy, VA_RT_FORMAT_YUV420, aw, ah, e->rec, 2, NULL, 0);
	if (s != VA_STATUS_SUCCESS) { e->rec[0] = e->rec[1] = VA_INVALID_SURFACE; venc_close(e); return -6; }
	VASurfaceID all[3] = {e->src, e->rec[0], e->rec[1]};
	s = vaCreateContext(e->dpy, e->cfg, aw, ah, VA_PROGRESSIVE, all, 3, &e->ctx);
	if (s != VA_STATUS_SUCCESS) { e->ctx = VA_INVALID_ID; venc_close(e); return -7; }
	e->coded_size = aw * ah * 3 / 2 + 65536;
	s = vaCreateBuffer(e->dpy, e->ctx, VAEncCodedBufferType, e->coded_size, 1, NULL, &e->coded);
	if (s != VA_STATUS_SUCCESS) { e->coded = VA_INVALID_ID; venc_close(e); return -8; }
	s = vaDeriveImage(e->dpy, e->src, &e->img);
	if (s == VA_STATUS_SUCCESS && e->img.format.fourcc == VA_FOURCC_NV12) {
		e->derived = 1;
	} else {
		if (s == VA_STATUS_SUCCESS) vaDestroyImage(e->dpy, e->img.image_id);
		VAImageFormat f;
		memset(&f, 0, sizeof(f));
		f.fourcc = VA_FOURCC_NV12; f.byte_order = VA_LSB_FIRST; f.bits_per_pixel = 12;
		s = vaCreateImage(e->dpy, &f, aw, ah, &e->img);
		if (s != VA_STATUS_SUCCESS) { venc_close(e); return -9; }
	}
	e->have_img = 1;
	e->rc_dirty = 1;
	if (vpp) venc_vpp_open(e, aw, ah);
	*out = e;
	return 0;
}

static int venc_map(venc *e, uint8_t **y, uint8_t **uv, int *ys, int *uvs) {
	void *p;
	if (vaMapBuffer(e->dpy, e->img.buf, &p) != VA_STATUS_SUCCESS) return -1;
	*y = (uint8_t *)p + e->img.offsets[0];
	*uv = (uint8_t *)p + e->img.offsets[1];
	*ys = e->img.pitches[0];
	*uvs = e->img.pitches[1];
	return 0;
}

static int venc_unmap(venc *e, int x, int y, int w, int h) {
	if (vaUnmapBuffer(e->dpy, e->img.buf) != VA_STATUS_SUCCESS) return -1;
	if (e->derived || w <= 0 || h <= 0) return 0;
	if (vaPutImage(e->dpy, e->src, e->img.image_id, x, y, w, h, x, y, w, h) != VA_STATUS_SUCCESS) return -2;
	return 0;
}

static int add_buf(venc *e, VABufferType t, unsigned size, void *data, VABufferID *ids, int *n) {
	if (vaCreateBuffer(e->dpy, e->ctx, t, size, 1, data, &ids[*n]) != VA_STATUS_SUCCESS) return -1;
	(*n)++;
	return 0;
}

static int add_misc(venc *e, VAEncMiscParameterType t, void *data, unsigned size, VABufferID *ids, int *n) {
	unsigned total = sizeof(VAEncMiscParameterBuffer) + size;
	uint8_t *b = calloc(1, total);
	VAEncMiscParameterBuffer *m = (VAEncMiscParameterBuffer *)b;
	m->type = t;
	memcpy(m->data, data, size);
	int r = add_buf(e, VAEncMiscParameterBufferType, total, b, ids, n);
	free(b);
	return r;
}

static void invalid_pic(VAPictureH264 *p) {
	p->picture_id = VA_INVALID_SURFACE;
	p->frame_idx = 0;
	p->flags = VA_PICTURE_H264_INVALID;
	p->TopFieldOrderCnt = 0;
	p->BottomFieldOrderCnt = 0;
}

static int venc_encode(venc *e, int idr, uint8_t *out, int cap, int *key) {
	if (venc_convert(e)) return -20;
	if (!e->have_ref) idr = 1;
	if (idr) { e->frame_num = 0; e->poc = 0; }
	VABufferID ids[16];
	int n = 0, r = -10;
	if (idr) {
		VAEncSequenceParameterBufferH264 sp;
		memset(&sp, 0, sizeof(sp));
		sp.seq_parameter_set_id = 0;
		sp.level_idc = level_idc(e);
		sp.intra_period = 0;
		sp.intra_idr_period = 0;
		sp.ip_period = 1;
		sp.bits_per_second = e->kbps * 1000;
		sp.max_num_ref_frames = 1;
		sp.picture_width_in_mbs = e->mbw;
		sp.picture_height_in_mbs = e->mbh;
		sp.seq_fields.bits.chroma_format_idc = 1;
		sp.seq_fields.bits.frame_mbs_only_flag = 1;
		sp.seq_fields.bits.direct_8x8_inference_flag = 1;
		sp.seq_fields.bits.log2_max_frame_num_minus4 = LOG2_MAX_FRAME_NUM - 4;
		sp.seq_fields.bits.pic_order_cnt_type = 2;
		if (e->mbw * 16 != e->w || e->mbh * 16 != e->h) {
			sp.frame_cropping_flag = 1;
			sp.frame_crop_right_offset = (e->mbw * 16 - e->w) / 2;
			sp.frame_crop_bottom_offset = (e->mbh * 16 - e->h) / 2;
		}
		sp.vui_parameters_present_flag = 1;
		sp.vui_fields.bits.timing_info_present_flag = 1;
		sp.num_units_in_tick = 1;
		sp.time_scale = e->fps * 2;
		if (add_buf(e, VAEncSequenceParameterBufferType, sizeof(sp), &sp, ids, &n)) goto out;
	}
	if (idr || e->rc_dirty) {
		VAEncMiscParameterRateControl rc;
		memset(&rc, 0, sizeof(rc));
		rc.bits_per_second = e->kbps * 1000;
		rc.target_percentage = 60;
		rc.window_size = 1000;
		rc.initial_qp = 26;
		rc.min_qp = e->minqp;
		rc.max_qp = 44;
		if (add_misc(e, VAEncMiscParameterTypeRateControl, &rc, sizeof(rc), ids, &n)) goto out;
		VAEncMiscParameterHRD hrd;
		memset(&hrd, 0, sizeof(hrd));
		hrd.buffer_size = e->kbps * 1000;
		hrd.initial_buffer_fullness = e->kbps * 500;
		if (add_misc(e, VAEncMiscParameterTypeHRD, &hrd, sizeof(hrd), ids, &n)) goto out;
		VAEncMiscParameterFrameRate fr;
		memset(&fr, 0, sizeof(fr));
		fr.framerate = e->fps;
		if (add_misc(e, VAEncMiscParameterTypeFrameRate, &fr, sizeof(fr), ids, &n)) goto out;
		e->rc_dirty = 0;
	}
	int cur = e->cur;
	VAEncPictureParameterBufferH264 pp;
	memset(&pp, 0, sizeof(pp));
	pp.CurrPic.picture_id = e->rec[cur];
	pp.CurrPic.frame_idx = e->frame_num;
	pp.CurrPic.TopFieldOrderCnt = e->poc * 2;
	pp.CurrPic.BottomFieldOrderCnt = e->poc * 2;
	for (int i = 0; i < 16; i++) invalid_pic(&pp.ReferenceFrames[i]);
	VAPictureH264 ref;
	invalid_pic(&ref);
	if (!idr) {
		ref.picture_id = e->rec[cur ^ 1];
		ref.frame_idx = e->ref_frame_num;
		ref.flags = VA_PICTURE_H264_SHORT_TERM_REFERENCE;
		ref.TopFieldOrderCnt = e->ref_poc * 2;
		ref.BottomFieldOrderCnt = e->ref_poc * 2;
		pp.ReferenceFrames[0] = ref;
	}
	pp.coded_buf = e->coded;
	pp.frame_num = e->frame_num;
	pp.pic_init_qp = 26;
	pp.num_ref_idx_l0_active_minus1 = 0;
	pp.pic_fields.bits.idr_pic_flag = idr ? 1 : 0;
	pp.pic_fields.bits.reference_pic_flag = 1;
	pp.pic_fields.bits.deblocking_filter_control_present_flag = 1;
	if (add_buf(e, VAEncPictureParameterBufferType, sizeof(pp), &pp, ids, &n)) goto out;
	if (idr) {
		bw w;
		memset(&w, 0, sizeof(w));
		write_sps(e, &w);
		if (add_packed(e, VAEncPackedHeaderSequence, &w, 0, ids, &n)) goto out;
		memset(&w, 0, sizeof(w));
		write_pps(&w);
		if (add_packed(e, VAEncPackedHeaderPicture, &w, 0, ids, &n)) goto out;
	}
	VAEncSliceParameterBufferH264 sl;
	memset(&sl, 0, sizeof(sl));
	sl.macroblock_address = 0;
	sl.num_macroblocks = e->mbw * e->mbh;
	sl.slice_type = idr ? 2 : 0;
	sl.disable_deblocking_filter_idc = 0;
	sl.pic_parameter_set_id = 0;
	sl.idr_pic_id = e->idr_id;
	for (int i = 0; i < 32; i++) { invalid_pic(&sl.RefPicList0[i]); invalid_pic(&sl.RefPicList1[i]); }
	if (!idr) sl.RefPicList0[0] = ref;
	sl.num_ref_idx_l0_active_minus1 = 0;
	if (add_buf(e, VAEncSliceParameterBufferType, sizeof(sl), &sl, ids, &n)) goto out;
	bw sh;
	memset(&sh, 0, sizeof(sh));
	write_slice_header(e, &sh, idr);
	if (add_packed(e, VAEncPackedHeaderSlice, &sh, 1, ids, &n)) goto out;
	if (vaBeginPicture(e->dpy, e->ctx, e->src) != VA_STATUS_SUCCESS) { r = -11; goto out; }
	if (vaRenderPicture(e->dpy, e->ctx, ids, n) != VA_STATUS_SUCCESS) { vaEndPicture(e->dpy, e->ctx); r = -12; goto out; }
	if (vaEndPicture(e->dpy, e->ctx) != VA_STATUS_SUCCESS) { r = -13; goto out; }
	if (vaSyncSurface(e->dpy, e->src) != VA_STATUS_SUCCESS) { r = -14; goto out; }
	VACodedBufferSegment *seg;
	if (vaMapBuffer(e->dpy, e->coded, (void **)&seg) != VA_STATUS_SUCCESS) { r = -15; goto out; }
	int off = 0;
	r = 0;
	for (; seg; seg = (VACodedBufferSegment *)seg->next) {
		if (seg->status & VA_CODED_BUF_STATUS_SLICE_OVERFLOW_MASK) { r = -16; break; }
		if (off + (int)seg->size > cap) { r = -17; break; }
		memcpy(out + off, seg->buf, seg->size);
		off += seg->size;
	}
	vaUnmapBuffer(e->dpy, e->coded);
	if (r == 0) {
		r = off;
		*key = idr;
		e->have_ref = 1;
		e->ref_frame_num = e->frame_num;
		e->ref_poc = e->poc;
		e->cur ^= 1;
		e->frame_num = (e->frame_num + 1) & ((1 << LOG2_MAX_FRAME_NUM) - 1);
		e->poc++;
		if (idr) e->idr_id++;
	}
out:
	for (int i = 0; i < n; i++) vaDestroyBuffer(e->dpy, ids[i]);
	return r;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"
)

type VAAPIEncoder struct {
	mu   sync.Mutex
	e    *C.venc
	w, h int
	out  []byte
}

func vaapiDevice() string {
	if d := os.Getenv("AGORA_VAAPI_DEVICE"); d != "" {
		return d
	}
	return "/dev/dri/renderD128"
}

func NewVAAPI(o H264Options) (*VAAPIEncoder, error) {
	o = o.withDefaults()
	w, h := o.Width&^1, o.Height&^1
	if w < 32 || h < 32 || w > 4096 || h > 4096 {
		return nil, fmt.Errorf("vaapi: unsupported size %dx%d", w, h)
	}
	dev := C.CString(vaapiDevice())
	defer C.free(unsafe.Pointer(dev))
	var e *C.venc
	vpp := 1
	if os.Getenv("AGORA_VAAPI_VPP") == "0" {
		vpp = 0
	}
	if r := C.venc_open(dev, C.int(w), C.int(h), C.int(o.FPS), C.int(o.MaxKbps), C.int(minQP(o.CRF)), C.int(vpp), &e); r != 0 {
		return nil, &vaapiOpenError{int(r)}
	}
	x := &VAAPIEncoder{e: e, w: w, h: h, out: make([]byte, int(e.coded_size))}
	return x, nil
}

type vaapiOpenError struct{ code int }

func (e *vaapiOpenError) Error() string { return fmt.Sprintf("vaapi: open failed (%d)", e.code) }

func (e *vaapiOpenError) unsupported() bool { return e.code >= -4 }

func minQP(crf int) int {
	if crf < 24 {
		return 20
	}
	return crf - 4
}

func (x *VAAPIEncoder) Name() string { return "vaapi" }

func (x *VAAPIEncoder) Size() (int, int) { return x.w, x.h }

func (x *VAAPIEncoder) Reconfigure(crf, maxKbps int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e == nil {
		return errors.New("closed")
	}
	x.e.kbps = C.int(maxKbps)
	x.e.minqp = C.int(minQP(crf))
	x.e.rc_dirty = 1
	return nil
}

func (x *VAAPIEncoder) planes() (y, uv []byte, ys, uvs int, err error) {
	var py, puv *C.uint8_t
	var cys, cuvs C.int
	if C.venc_map(x.e, &py, &puv, &cys, &cuvs) != 0 {
		return nil, nil, 0, 0, errors.New("vaapi: map failed")
	}
	ah := int(x.e.mbh) * 16
	ys, uvs = int(cys), int(cuvs)
	y = unsafe.Slice((*byte)(unsafe.Pointer(py)), ys*ah)
	uv = unsafe.Slice((*byte)(unsafe.Pointer(puv)), uvs*ah/2)
	return
}

func (x *VAAPIEncoder) Encode(bgrx []byte, stride, srcW, srcH int, ptsMs int64, idr bool) ([]byte, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e == nil {
		return nil, false, errors.New("closed")
	}
	if x.e.vpp != 0 {
		if err := x.copyRGB(bgrx, stride, 0, 0, min(srcW, x.w), min(srcH, x.h)); err != nil {
			return nil, false, err
		}
		return x.encode(idr)
	}
	y, uv, ys, uvs, err := x.planes()
	if err != nil {
		return nil, false, err
	}
	cerr := BGRXToNV12(bgrx, stride, srcW, srcH, y, ys, uv, uvs, x.w, x.h)
	if C.venc_unmap(x.e, 0, 0, C.int(x.w), C.int(x.h)) != 0 {
		return nil, false, errors.New("vaapi: upload failed")
	}
	if cerr != nil {
		return nil, false, cerr
	}
	return x.encode(idr)
}

func (x *VAAPIEncoder) EncodeRect(src []byte, stride, rx, ry, rw, rh int, ptsMs int64, idr bool) ([]byte, bool, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e == nil {
		return nil, false, errors.New("closed")
	}
	if rw > 0 && rh > 0 && rx >= 0 && ry >= 0 && rx+rw <= x.w && ry+rh <= x.h && x.e.vpp != 0 {
		if err := x.copyRGB(src, stride, rx, ry, rw, rh); err != nil {
			return nil, false, err
		}
	} else if rw > 0 && rh > 0 && rx >= 0 && ry >= 0 && rx+rw <= x.w && ry+rh <= x.h && rx%2 == 0 && ry%2 == 0 && rw%2 == 0 && rh%2 == 0 {
		y, uv, ys, uvs, err := x.planes()
		if err != nil {
			return nil, false, err
		}
		cerr := BGRXRectToNV12(src, stride, rx, ry, rw, rh, y, ys, uv, uvs)
		if C.venc_unmap(x.e, C.int(rx), C.int(ry), C.int(rw), C.int(rh)) != 0 {
			return nil, false, errors.New("vaapi: upload failed")
		}
		if cerr != nil {
			return nil, false, cerr
		}
	}
	return x.encode(idr)
}

func (x *VAAPIEncoder) copyRGB(src []byte, stride, rx, ry, rw, rh int) error {
	if !fits(src, stride, rw, rh) {
		return ErrShortFrame
	}
	var p *C.uint8_t
	var cs C.int
	if C.venc_map_rgb(x.e, &p, &cs) != 0 {
		return errors.New("vaapi: map failed")
	}
	ds := int(cs)
	dst := unsafe.Slice((*byte)(unsafe.Pointer(p)), ds*(int(x.e.mbh)*16))
	for row := 0; row < rh; row++ {
		copy(dst[(ry+row)*ds+rx*4:(ry+row)*ds+(rx+rw)*4], src[row*stride:row*stride+rw*4])
	}
	if C.venc_unmap_rgb(x.e) != 0 {
		return errors.New("vaapi: upload failed")
	}
	return nil
}

func (x *VAAPIEncoder) encode(idr bool) ([]byte, bool, error) {
	var key C.int
	idrI := 0
	if idr {
		idrI = 1
	}
	n := C.venc_encode(x.e, C.int(idrI), (*C.uint8_t)(unsafe.Pointer(&x.out[0])), C.int(len(x.out)), &key)
	if n < 0 {
		return nil, false, fmt.Errorf("vaapi: encode failed (%d)", int(n))
	}
	if n == 0 {
		return nil, false, nil
	}
	b := make([]byte, int(n))
	copy(b, x.out[:n])
	return b, key != 0, nil
}

func (x *VAAPIEncoder) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.e != nil {
		C.venc_close(x.e)
		x.e = nil
	}
}
